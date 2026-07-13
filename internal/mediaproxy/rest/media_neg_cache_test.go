package rest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
)

// AUDIT-S2 (F-03): the SECOND request for a permanently-unknown hash must NOT
// re-run the expensive grace loop. Request 1 (neg cache empty) pays the full
// grace scan (Serve-level Get + ~10 poll Gets) then neg-caches the hash on
// expiry. Request 2 short-circuits: only the single Serve-level repo.Get runs
// (the grace re-scan is skipped), proven by the exact repo.Get delta == 1 and a
// grace_total{outcome="skipped"} tick with no new grace_total{outcome="expired"}.
func TestMediaHandler_Grace_SecondUnknownRequestSkipsGraceLoop(t *testing.T) {
	h, repo, _ := newHandler(t)
	h.graceRetryBudget = 20 * time.Millisecond
	h.graceRetryInterval = 2 * time.Millisecond
	h.unknownNeg.ttl = time.Hour                                        // keep the negative entry live across both requests
	hash := hashOf("https://image.tmdb.org/t/p/w342/perma-unknown.jpg") // no row, never appears

	r := newRouter(h)

	beforeExpired := graceCounter("expired")
	beforeSkipped := graceCounter("skipped")

	// Request 1: full grace loop → expiry → placeholder + neg-cache the hash.
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr1.Code != http.StatusOK || rr1.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatalf("req1 want 200 placeholder, got %d ph=%q", rr1.Code, rr1.Header().Get("X-Media-Placeholder"))
	}
	callsAfter1 := repo.getCalls.Load()
	if callsAfter1 < 2 {
		t.Fatalf("req1 must run the grace loop (Serve Get + >=1 poll Get), got %d total repo.Get", callsAfter1)
	}
	if got := graceCounter("expired") - beforeExpired; got != 1 {
		t.Fatalf("req1 grace expired delta want 1, got %d", got)
	}
	if !h.unknownNeg.contains(hash) {
		t.Fatal("req1 must populate the unknown-hash negative cache on expiry")
	}

	// Request 2: neg cache hit → grace loop SKIPPED. Only the Serve-level Get runs.
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr2.Code != http.StatusOK || rr2.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatalf("req2 want 200 placeholder, got %d ph=%q", rr2.Code, rr2.Header().Get("X-Media-Placeholder"))
	}
	if callsForReq2 := repo.getCalls.Load() - callsAfter1; callsForReq2 != 1 {
		t.Fatalf("req2 must skip the grace loop: want exactly 1 repo.Get (the Serve-level lookup), got %d", callsForReq2)
	}
	if got := graceCounter("skipped") - beforeSkipped; got != 1 {
		t.Fatalf("req2 grace skipped delta want 1, got %d", got)
	}
	if got := graceCounter("expired") - beforeExpired; got != 1 {
		t.Fatalf("req2 must NOT add another expired (still 1 total across both), got %d", got)
	}
}

// AUDIT-S2 (F-03): the negative entry is SHORT-TTL — grace-retry's purpose is
// preserved. A hash neg-cached on one request must still resolve to real bytes
// once the entry expires and the row exists. This proves the cache never
// permanently masks a hash that becomes known.
func TestMediaHandler_Grace_NegCacheExpiresThenResolves(t *testing.T) {
	h, repo, store := newHandler(t)
	h.graceRetryBudget = 10 * time.Millisecond
	h.graceRetryInterval = 2 * time.Millisecond
	h.unknownNeg.ttl = 30 * time.Millisecond // short so it self-heals quickly
	url := "https://image.tmdb.org/t/p/w342/heals-after-ttl.jpg"
	hash := hashOf(url)

	r := newRouter(h)

	// Request 1: no row → grace expires → hash neg-cached, placeholder served.
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr1.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatalf("req1 want placeholder, got %q", rr1.Header().Get("X-Media-Placeholder"))
	}
	if !h.unknownNeg.contains(hash) {
		t.Fatal("req1 must populate the unknown-hash negative cache")
	}

	// The row lands (stored) and the neg-cache TTL elapses.
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, extForCT("image/jpeg")), bytes.NewReader([]byte("PNG")), 3, "image/jpeg")
	time.Sleep(40 * time.Millisecond) // > ttl → the negative entry expires

	// Request 2: neg entry expired → not short-circuited → Serve finds the stored
	// row → real bytes, NOT the placeholder.
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("req2 want 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	if rr2.Header().Get("X-Media-Placeholder") != "" {
		t.Fatal("req2 after TTL expiry must resolve to real bytes, not the placeholder")
	}
	if rr2.Body.String() != "PNG" {
		t.Fatalf("req2 want real bytes PNG, got %q", rr2.Body.String())
	}
}
