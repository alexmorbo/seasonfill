package tmdb

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

// newREDClient builds a white-box Client pointed at a test server. RPS is set
// high so the token bucket never delays the metric-shape assertions — the only
// waits the test tolerates are the retry-backoff sleeps on the 429/5xx/transport
// paths (control flow we are NOT allowed to change).
func newREDClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:    base,
		Token:      "tk",
		Language:   "en-US",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		RPS:        1000,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestTMDBREDMetrics drives every RED branch through httptest servers and
// asserts each seasonfill_tmdb_* series (with the NORMALISED endpoint family)
// is exported. Deterministic — no real TMDB, no wall-clock value assertions.
func TestTMDBREDMetrics(t *testing.T) {
	// ---- Phase 1: 200 OK — duration, requests_total{2xx}, lane_wait{batch,
	// interactive}, and the CRUCIAL endpoint normalisation (/tv/90399 → /tv/{id}).
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer okSrv.Close()
	okClient := newREDClient(t, okSrv.URL)
	defer okClient.Close()

	// Batch lane (bare ctx) + concrete id 90399 → endpoint must normalise to /tv/{id}.
	if _, err := okClient.GetTV(context.Background(), 90399, ""); err != nil {
		t.Fatalf("batch GetTV: %v", err)
	}
	// Interactive lane: distinct id 90400 → SWR miss → sync fetch forwards the
	// caller ctx to doDirect, so the interactive marker survives (swr.go:195).
	if _, err := okClient.GetTV(WithInteractive(context.Background()), 90400, ""); err != nil {
		t.Fatalf("interactive GetTV: %v", err)
	}

	// ---- Phase 2: 429 exhaustion — requests_total{429}, retries_total{rate_limited}.
	tooManySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1") // cap backoff + global pause at ~1s
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer tooManySrv.Close()
	rlClient := newREDClient(t, tooManySrv.URL)
	defer rlClient.Close()
	_, _ = rlClient.GetTV(context.Background(), 90399, "") // 3 attempts → terminal 429

	// ---- Phase 3: 500 exhaustion — requests_total{5xx}, retries_total{server_error}.
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()
	srvErrClient := newREDClient(t, srv500.URL)
	defer srvErrClient.Close()
	_, _ = srvErrClient.GetTV(context.Background(), 90399, "")

	// ---- Phase 4: transport error — requests_total{error}, retries_total{transport}.
	// A server closed immediately after construction → Do() returns
	// connection-refused (no HTTP response), the "error" status_class + the
	// "transport" retry reason.
	deadSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := deadSrv.URL
	deadSrv.Close()
	deadClient := newREDClient(t, deadURL)
	defer deadClient.Close()
	_, _ = deadClient.GetTV(context.Background(), 90399, "")

	// ---- Scrape once; assert presence of every required series. ----
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()

	mustContain := func(sub string) {
		t.Helper()
		if !strings.Contains(body, sub) {
			t.Fatalf("metrics missing %q\n--- /metrics ---\n%s", sub, body)
		}
	}
	mustAbsent := func(sub string) {
		t.Helper()
		if strings.Contains(body, sub) {
			t.Fatalf("metrics unexpectedly contains %q (endpoint not normalised)\n--- /metrics ---\n%s", sub, body)
		}
	}

	// Duration histogram — NORMALISED endpoint family present, concrete id absent.
	mustContain(`seasonfill_tmdb_request_duration_seconds_count{endpoint="/tv/{id}"}`)
	mustAbsent(`endpoint="/tv/90399"`)
	mustAbsent(`endpoint="/tv/90400"`)

	// requests_total per status_class (all four reachable classes).
	mustContain(`seasonfill_tmdb_requests_total{endpoint="/tv/{id}",status_class="2xx"}`)
	mustContain(`seasonfill_tmdb_requests_total{endpoint="/tv/{id}",status_class="429"}`)
	mustContain(`seasonfill_tmdb_requests_total{endpoint="/tv/{id}",status_class="5xx"}`)
	mustContain(`seasonfill_tmdb_requests_total{endpoint="/tv/{id}",status_class="error"}`)

	// retries_total per reason.
	mustContain(`seasonfill_tmdb_retries_total{reason="rate_limited"}`)
	mustContain(`seasonfill_tmdb_retries_total{reason="server_error"}`)
	mustContain(`seasonfill_tmdb_retries_total{reason="transport"}`)

	// lane_wait per lane.
	mustContain(`seasonfill_tmdb_lane_wait_seconds_count{lane="batch"}`)
	mustContain(`seasonfill_tmdb_lane_wait_seconds_count{lane="interactive"}`)
}
