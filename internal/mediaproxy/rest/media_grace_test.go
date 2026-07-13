package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
)

// graceCounter reads the current value of the per-outcome grace-retry counter
// directly off the default metrics set — same instance observability.
// IncMediaServeGrace bumps. Used for before/after deltas so assertions survive
// other tests in this binary hitting the grace paths.
func graceCounter(outcome string) uint64 {
	return metrics.GetOrCreateCounter(
		`seasonfill_media_serve_grace_total{outcome="` + outcome + `"}`,
	).Get()
}

// Row absent on the first Get, then the deferred EnsurePending "goroutine" lands
// the pending row on the 3rd Get INSIDE the grace window → the handler rejoins the
// normal serveOnDemand → store path and serves the REAL bytes (200 + real
// content-type/body, NOT the SVG placeholder). grace_total{resolved} ticks once.
func TestMediaHandler_Grace_RowAppearsWithinWindow_ServesRealBytes(t *testing.T) {
	url := "https://image.tmdb.org/t/p/w342/grace-appears.jpg"
	hash := hashOf(url)
	resolver := newStubPendingResolver()
	resolver.put(hash, url, "poster_w342", media.StatusPending)
	fetcher := &stubOnDemand{hashWin: hash, bytes: []byte("PNG"), contentT: "image/jpeg"}
	h, repo, store := newOnDemandHandler(t, resolver, fetcher)
	fetcher.store = store
	fetcher.repo = repo
	// Row ABSENT initially; appears (status=pending) on the 3rd repo.Get.
	repo.appearAtCall = 3
	repo.appearAsset = media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusPending}
	h.graceRetryBudget = 20 * time.Millisecond
	h.graceRetryInterval = 2 * time.Millisecond

	r := newRouter(h)
	rr := httptest.NewRecorder()
	before := graceCounter("resolved")
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "PNG" {
		t.Fatalf("want real bytes PNG, got %q", rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/jpeg") {
		t.Fatalf("want real asset Content-Type image/jpeg, got %q", got)
	}
	if rr.Header().Get("X-Media-Placeholder") != "" {
		t.Fatal("row resolved during grace must NOT serve the placeholder")
	}
	if got := graceCounter("resolved") - before; got != 1 {
		t.Fatalf("grace resolved delta want 1, got %d", got)
	}
}

// Row absent for the ENTIRE grace window → unknown_hash placeholder after the
// budget elapses: SVG + X-Media-Placeholder:1 + degraded{unknown_hash} +
// grace_total{expired}. Also asserts the handler returns promptly (bounded).
func TestMediaHandler_Grace_RowAbsentEntireWindow_PlaceholderExpired(t *testing.T) {
	h, _, _ := newHandler(t)
	h.graceRetryBudget = 20 * time.Millisecond
	h.graceRetryInterval = 2 * time.Millisecond
	hash := hashOf("https://image.tmdb.org/t/p/w342/grace-never.jpg") // no row, never appears

	r := newRouter(h)
	rr := httptest.NewRecorder()
	beforeExpired := graceCounter("expired")
	beforeDeg := degradedCounter("unknown_hash")
	start := time.Now()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 placeholder, got %d", rr.Code)
	}
	if rr.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatal("expected X-Media-Placeholder=1 after grace expiry")
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Fatalf("want image/svg+xml, got %q", got)
	}
	if !strings.Contains(rr.Body.String(), "<svg") {
		t.Fatalf("expected SVG body, got %q", rr.Body.String())
	}
	if got := graceCounter("expired") - beforeExpired; got != 1 {
		t.Fatalf("grace expired delta want 1, got %d", got)
	}
	if got := degradedCounter("unknown_hash") - beforeDeg; got != 1 {
		t.Fatalf("unknown_hash degraded delta want 1, got %d", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("grace expiry hung %s past the %s budget", elapsed, h.graceRetryBudget)
	}
}

// Request ctx cancelled before/at grace entry → the handler bails WITHOUT writing
// anything (no placeholder body, no header) and does NOT count grace expired,
// mirroring the clientGone silent-return of the serve + on-demand error branches.
func TestMediaHandler_Grace_ClientAbortWritesNothing(t *testing.T) {
	h, _, _ := newHandler(t)
	h.graceRetryBudget = 50 * time.Millisecond
	h.graceRetryInterval = 5 * time.Millisecond
	hash := hashOf("https://image.tmdb.org/t/p/w342/grace-abort.jpg") // no row

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client gone before the grace loop runs

	r := newRouter(h)
	rr := httptest.NewRecorder()
	beforeExpired := graceCounter("expired")
	r.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/media/"+hash, nil))

	if rr.Header().Get("X-Media-Placeholder") != "" {
		t.Fatal("client abort mid-grace must NOT render a placeholder")
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("client abort must write no body, got %q", rr.Body.String())
	}
	if got := graceCounter("expired") - beforeExpired; got != 0 {
		t.Fatalf("client abort must NOT count grace expired, delta %d", got)
	}
}
