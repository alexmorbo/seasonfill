package rest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
)

func TestMedia_Direct_Redirect(t *testing.T) {
	h, repo, store := newHandler(t)
	h.SetMediaDirect(true)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	// Bytes exist in the store too — direct mode must NOT touch the store.
	_ = store.Put(context.Background(), mediastore.Key(url, extForCT("image/jpeg")), bytes.NewReader([]byte("PNG")), 3, "image/jpeg")
	r := newRouter(h)

	before := store.calls.Load()
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	if rr.Code != http.StatusFound {
		t.Fatalf("want 302 got %d", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != url {
		t.Fatalf("Location = %q want %q", got, url)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if store.calls.Load() != before {
		t.Fatal("direct mode hit the store — must not stream the blob")
	}
}

func TestMedia_Direct_NeverRedirectsSentinel(t *testing.T) {
	h, _, _ := newHandler(t)
	h.SetMediaDirect(true)
	r := newRouter(h)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/media/"+appmedia.SentinelMissingHash, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("sentinel must stay 200 SVG in direct mode, got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "" {
		t.Fatal("sentinel was redirected — must never happen")
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Fatalf("sentinel content-type = %q want image/svg+xml", ct)
	}
}

func TestMedia_Direct_NoRowFallsThroughToPlaceholder(t *testing.T) {
	h, _, _ := newHandler(t) // empty repo → ErrNotFound
	h.SetMediaDirect(true)
	url := "https://image.tmdb.org/t/p/w342/missing.jpg"
	hash := hashOf(url)
	r := newRouter(h)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	// No row → NOT a redirect; falls through to the SVG placeholder (200).
	if rr.Code == http.StatusFound {
		t.Fatal("direct mode 302'd a hash with no media_assets row")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("want placeholder 200 got %d", rr.Code)
	}
}

func TestMedia_Direct_HotReloadFlip(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, extForCT("image/jpeg")), bytes.NewReader([]byte("PNG")), 3, "image/jpeg")
	r := newRouter(h)

	// Default (proxy) → 200 bytes.
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr1.Code != http.StatusOK {
		t.Fatalf("proxy default: want 200 got %d", rr1.Code)
	}

	// Flip to direct → 302 on the very next request (no restart).
	h.SetMediaDirect(true)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr2.Code != http.StatusFound {
		t.Fatalf("after flip: want 302 got %d", rr2.Code)
	}

	// Flip back → proxy 200 again.
	h.SetMediaDirect(false)
	rr3 := httptest.NewRecorder()
	r.ServeHTTP(rr3, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr3.Code != http.StatusOK {
		t.Fatalf("after flip-back: want 200 got %d", rr3.Code)
	}
}
