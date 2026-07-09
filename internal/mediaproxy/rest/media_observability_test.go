package rest

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/VictoriaMetrics/metrics"

	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
)

var errStoreDown = errors.New("s3 saturated: connection reset by peer")

func sentinelHashForTest() string { return appmedia.SentinelMissingHash }

// safeWriter serializes writes into the underlying builder.
type safeWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *safeWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// degradedCounter reads the current value of the per-reason degraded
// counter directly off the default metrics set — same counter instance
// observability.IncMediaServeDegraded bumps (dedup keyed on the metric
// string). Used for before/after deltas so the assertions survive other
// tests in this binary hitting placeholder paths.
func degradedCounter(reason string) uint64 {
	return metrics.GetOrCreateCounter(
		`seasonfill_media_serve_degraded_total{reason="` + reason + `"}`,
	).Get()
}

// newHandlerWithLogger builds a handler with an explicit logger so the
// Info placeholder/sentinel log can be captured into a buffer.
func newHandlerWithLogger(logger *slog.Logger) (*MediaHandler, *stubRepo, *stubStore) {
	repo := newStubRepo()
	store := newStubStore()
	h := NewMediaHandler(MediaHandlerDeps{
		Store:      store,
		Repo:       repo,
		HTTPClient: http.DefaultClient,
		Logger:     logger,
	})
	return h, repo, store
}

// Placeholder log is Info-level and carries reason, asset_status, and
// elapsed_ms. Exercised via the unknown_hash branch (no repo row →
// asset_status "none").
func TestMediaHandler_Placeholder_LogsInfoReasonStatusElapsed(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&safeWriter{w: &buf}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h, _, _ := newHandlerWithLogger(logger)

	hash := hashOf("https://image.tmdb.org/t/p/w342/unknown-obs.jpg") // no row → unknown_hash
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 placeholder, got %d", rr.Code)
	}
	line := buf.String()
	for _, want := range []string{
		`"level":"INFO"`,
		`"msg":"media.serve.placeholder"`,
		`"reason":"unknown_hash"`,
		`"asset_status":"none"`,
		`"elapsed_ms":`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("placeholder log missing %s\nlog: %s", want, line)
		}
	}
}

// Placeholder increments the degraded counter for a non-store_unavailable
// reason (unknown_hash) exactly once per served placeholder.
func TestMediaHandler_Placeholder_IncrementsDegradedMetric(t *testing.T) {
	h, _, _ := newHandler(t)
	hash := hashOf("https://image.tmdb.org/t/p/w342/metric-unknown.jpg") // unknown_hash
	r := newRouter(h)

	before := degradedCounter("unknown_hash")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if got := degradedCounter("unknown_hash") - before; got != 1 {
		t.Fatalf("unknown_hash degraded counter delta want 1, got %d", got)
	}
}

// store_unavailable is counted exactly once (choke point inside
// writePlaceholder), NOT twice — proves the standalone IncMediaServeDegraded
// was removed. Also asserts the log carries asset_status="stored".
func TestMediaHandler_StoreUnavailable_NoDoubleCount(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&safeWriter{w: &buf}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h, repo, store := newHandlerWithLogger(logger)

	url := "https://image.tmdb.org/t/p/w342/store-obs.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	store.getErr = errStoreDown // non-NotFound store error → store_unavailable branch

	r := newRouter(h)
	before := degradedCounter("store_unavailable")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 placeholder, got %d", rr.Code)
	}
	if got := degradedCounter("store_unavailable") - before; got != 1 {
		t.Fatalf("store_unavailable counter delta want 1 (no double-count), got %d", got)
	}
	line := buf.String()
	if !strings.Contains(line, `"reason":"store_unavailable"`) || !strings.Contains(line, `"asset_status":"stored"`) {
		t.Fatalf("store_unavailable placeholder log missing reason/asset_status\nlog: %s", line)
	}
	if !strings.Contains(line, `"elapsed_ms":`) {
		t.Fatalf("store_unavailable placeholder log missing elapsed_ms\nlog: %s", line)
	}
}

// Sentinel logs at Info with elapsed_ms and is deliberately EXCLUDED from
// the degraded metric.
func TestMediaHandler_Sentinel_LogsInfoElapsed_NotCounted(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&safeWriter{w: &buf}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h, _, _ := newHandlerWithLogger(logger)
	r := newRouter(h)

	// No "sentinel" reason is ever passed to IncMediaServeDegraded, so this
	// counter must stay flat across the sentinel request.
	before := degradedCounter("sentinel")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/media/"+sentinelHashForTest(), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if got := degradedCounter("sentinel") - before; got != 0 {
		t.Fatalf("sentinel must NOT bump degraded counter, delta=%d", got)
	}
	line := buf.String()
	if !strings.Contains(line, `"msg":"media.serve.sentinel"`) || !strings.Contains(line, `"level":"INFO"`) {
		t.Fatalf("sentinel log not Info-level media.serve.sentinel\nlog: %s", line)
	}
	if !strings.Contains(line, `"elapsed_ms":`) {
		t.Fatalf("sentinel log missing elapsed_ms\nlog: %s", line)
	}
}
