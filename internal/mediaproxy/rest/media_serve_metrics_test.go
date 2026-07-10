package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/metrics"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

// --- direct counter/gauge readers (mirror degradedCounter in media_observability_test.go) ---

func serveOutcomeCount(outcome string) uint64 {
	return metrics.GetOrCreateCounter(
		`seasonfill_media_serve_total{outcome="` + outcome + `"}`,
	).Get()
}

func lruHitCount() uint64 {
	return metrics.GetOrCreateCounter(`seasonfill_media_serve_lru_hits_total`).Get()
}

func lruMissCount() uint64 {
	return metrics.GetOrCreateCounter(`seasonfill_media_serve_lru_misses_total`).Get()
}

func serveBytesCount() uint64 {
	return metrics.GetOrCreateCounter(`seasonfill_media_serve_bytes_total`).Get()
}

// Cache-hit path: LRU miss then hit, outcome=stored twice, egress bytes counted on each
// 200. "PNG" body = 3 bytes → two 200s = 6 egress bytes.
func TestMediaMetrics_CacheHitStoredBytes(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/metrics-cachehit.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, extForCT("image/jpeg")), strings.NewReader("PNG"), 3, "image/jpeg")
	r := newRouter(h)

	beforeStored := serveOutcomeCount("stored")
	beforeHit := lruHitCount()
	beforeMiss := lruMissCount()
	beforeBytes := serveBytesCount()

	// 1st request → store serve (LRU miss).
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr1.Code != http.StatusOK {
		t.Fatalf("first: want 200, got %d", rr1.Code)
	}
	// 2nd request → LRU hit.
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("second: want 200, got %d", rr2.Code)
	}

	if got := serveOutcomeCount("stored") - beforeStored; got != 2 {
		t.Fatalf("stored outcome delta want 2, got %d", got)
	}
	if got := lruHitCount() - beforeHit; got != 1 {
		t.Fatalf("lru hit delta want 1, got %d", got)
	}
	if got := lruMissCount() - beforeMiss; got != 1 {
		t.Fatalf("lru miss delta want 1, got %d", got)
	}
	if got := serveBytesCount() - beforeBytes; got != 6 {
		t.Fatalf("egress bytes delta want 6 (two 3-byte 200s), got %d", got)
	}
	if serveBytesCount()-beforeBytes == 0 {
		t.Fatal("egress bytes must be > 0 on a body write")
	}

	// Assert the metric surfaces through WritePrometheus as required.
	var buf strings.Builder
	observability.WritePrometheus(&buf)
	out := buf.String()
	for _, want := range []string{
		`seasonfill_media_serve_total{outcome="stored"}`,
		`seasonfill_media_serve_lru_hits_total`,
		`seasonfill_media_serve_lru_misses_total`,
		`seasonfill_media_serve_bytes_total`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("WritePrometheus missing %q", want)
		}
	}
}

// 304 path: outcome=not_modified, no egress bytes.
func TestMediaMetrics_NotModified(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/metrics-304.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, extForCT("image/jpeg")), strings.NewReader("PNG"), 3, "image/jpeg")
	r := newRouter(h)

	// Prime the LRU with a plain GET.
	rr0 := httptest.NewRecorder()
	r.ServeHTTP(rr0, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	beforeNM := serveOutcomeCount("not_modified")
	beforeBytes := serveBytesCount()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil)
	req.Header.Set("If-None-Match", `"`+hash+`"`)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("want 304, got %d", rr.Code)
	}
	if got := serveOutcomeCount("not_modified") - beforeNM; got != 1 {
		t.Fatalf("not_modified delta want 1, got %d", got)
	}
	if got := serveBytesCount() - beforeBytes; got != 0 {
		t.Fatalf("304 must write no egress bytes, delta %d", got)
	}
}

// Placeholder path (unknown_hash): outcome=placeholder.
func TestMediaMetrics_PlaceholderOutcome(t *testing.T) {
	h, _, _ := newHandler(t)
	hash := hashOf("https://image.tmdb.org/t/p/w342/metrics-placeholder.jpg") // no row → unknown_hash
	r := newRouter(h)

	before := serveOutcomeCount("placeholder")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 placeholder, got %d", rr.Code)
	}
	if got := serveOutcomeCount("placeholder") - before; got != 1 {
		t.Fatalf("placeholder outcome delta want 1, got %d", got)
	}
}

// Degrade path (store_unavailable): outcome=degraded (NOT placeholder), and the legacy
// per-reason degraded metric still increments exactly once.
func TestMediaMetrics_DegradedOutcome(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/metrics-degraded.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	store.getErr = errStoreDown // non-NotFound store error → store_unavailable branch
	r := newRouter(h)

	beforeDeg := serveOutcomeCount("degraded")
	beforePH := serveOutcomeCount("placeholder")
	beforeLegacy := degradedCounter("store_unavailable")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 placeholder, got %d", rr.Code)
	}
	if got := serveOutcomeCount("degraded") - beforeDeg; got != 1 {
		t.Fatalf("degraded outcome delta want 1, got %d", got)
	}
	if got := serveOutcomeCount("placeholder") - beforePH; got != 0 {
		t.Fatalf("store_unavailable must map to degraded, not placeholder; placeholder delta %d", got)
	}
	if got := degradedCounter("store_unavailable") - beforeLegacy; got != 1 {
		t.Fatalf("legacy degraded{reason=store_unavailable} must still tick once, got %d", got)
	}
}

// Sentinel path: outcome=sentinel (counted in serve_total, still NOT in degraded_total).
func TestMediaMetrics_SentinelOutcome(t *testing.T) {
	h, _, _ := newHandler(t)
	r := newRouter(h)
	before := serveOutcomeCount("sentinel")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/media/"+sentinelHashForTest(), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if got := serveOutcomeCount("sentinel") - before; got != 1 {
		t.Fatalf("sentinel outcome delta want 1, got %d", got)
	}
}

// Invalid hash: outcome=invalid (400).
func TestMediaMetrics_InvalidOutcome(t *testing.T) {
	h, _, _ := newHandler(t)
	r := newRouter(h)
	before := serveOutcomeCount("invalid")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/not-a-hash", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
	if got := serveOutcomeCount("invalid") - before; got != 1 {
		t.Fatalf("invalid outcome delta want 1, got %d", got)
	}
}
