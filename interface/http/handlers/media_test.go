package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/media"
	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
)

// stubRepo for the handler tests.
type stubRepo struct {
	mu     sync.Mutex
	byHash map[string]media.Asset
}

func newStubRepo() *stubRepo { return &stubRepo{byHash: map[string]media.Asset{}} }

func (s *stubRepo) put(a media.Asset) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[a.Hash] = a
}

func (s *stubRepo) Get(ctx context.Context, hash string) (media.Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byHash[hash]
	if !ok {
		return media.Asset{}, ports.ErrNotFound
	}
	return a, nil
}

func (s *stubRepo) Upsert(ctx context.Context, a media.Asset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[a.Hash] = a
	return nil
}

// stubStore — in-memory mediastore.
type stubStore struct {
	mu    sync.Mutex
	body  map[string][]byte
	ct    map[string]string
	calls atomic.Int32
}

func newStubStore() *stubStore { return &stubStore{body: map[string][]byte{}, ct: map[string]string{}} }

func (s *stubStore) Get(ctx context.Context, key string) (io.ReadCloser, mediastore.ObjectInfo, error) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.body[key]
	if !ok {
		return nil, mediastore.ObjectInfo{}, mediastore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), mediastore.ObjectInfo{Key: key, Size: int64(len(b)), ContentType: s.ct[key]}, nil
}

func (s *stubStore) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	b, _ := io.ReadAll(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body[key] = b
	s.ct[key] = ct
	return nil
}

func (s *stubStore) Stat(ctx context.Context, key string) (mediastore.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.body[key]
	if !ok {
		return mediastore.ObjectInfo{}, mediastore.ErrNotFound
	}
	return mediastore.ObjectInfo{Key: key, Size: int64(len(b)), ContentType: s.ct[key]}, nil
}

func (s *stubStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.body, key)
	delete(s.ct, key)
	return nil
}

func (s *stubStore) List(ctx context.Context, prefix string, fn func(mediastore.ObjectInfo) error) error {
	return nil
}

func newHandler(t *testing.T) (*MediaHandler, *stubRepo, *stubStore) {
	t.Helper()
	repo := newStubRepo()
	store := newStubStore()
	h := NewMediaHandler(store, repo, http.DefaultClient, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	return h, repo, store
}

func newRouter(h *MediaHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/media/:hash", h.Serve)
	return r
}

// hashOf computes the sha256 hex of the given URL — mirrors the
// application/media.HashFromURL helper without the import.
func hashOf(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// extForCT mirrors handlers.extFromContentType so the test can build
// the mediastore key the production handler would resolve.
func extForCT(ct string) string {
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return ""
}

func TestMedia_LRUHit(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, extForCT("image/jpeg")), bytes.NewReader([]byte("PNG")), 3, "image/jpeg")
	r := newRouter(h)

	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr1.Code != 200 {
		t.Fatalf("first: code %d body=%s", rr1.Code, rr1.Body.String())
	}
	beforeCalls := store.calls.Load()

	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr2.Code != 200 {
		t.Fatalf("second: code %d", rr2.Code)
	}
	if store.calls.Load() != beforeCalls {
		t.Fatal("LRU miss: store.Get was called on the second hit")
	}
}

func TestMedia_NotModified(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, extForCT("image/jpeg")), bytes.NewReader([]byte("PNG")), 3, "image/jpeg")
	r := newRouter(h)

	// Prime LRU.
	rr0 := httptest.NewRecorder()
	r.ServeHTTP(rr0, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil)
	req.Header.Set("If-None-Match", `"`+hash+`"`)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("want 304 got %d", rr.Code)
	}
	if rr.Header().Get("ETag") == "" {
		t.Fatal("ETag must be set on 304")
	}
}

func TestMedia_LostObjectRecovery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("REFETCH"))
	}))
	defer upstream.Close()

	h, repo, store := newHandler(t)
	h.http = upstream.Client()
	url := upstream.URL + "/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 7, Status: media.StatusStored})
	// Store DELIBERATELY empty — simulates a lost object.

	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "REFETCH") {
		t.Fatalf("want 200 with REFETCH body, got code %d body %q", rr.Code, rr.Body.String())
	}
	// And the bytes are back in the store now.
	_, _, err := store.Get(context.Background(), mediastore.Key(url, "jpg"))
	if err != nil {
		t.Fatalf("after recovery want stored, got err %v", err)
	}
}

func TestMedia_PendingReturns404(t *testing.T) {
	h, repo, _ := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusPending})
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != 404 {
		t.Fatalf("want 404 got %d", rr.Code)
	}
}

func TestMedia_FailedReturns404(t *testing.T) {
	h, repo, _ := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusFailed})
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != 404 {
		t.Fatalf("want 404 got %d", rr.Code)
	}
}

func TestMedia_InvalidHashReturns400(t *testing.T) {
	h, _, _ := newHandler(t)
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/not-a-hash", nil))
	if rr.Code != 400 {
		t.Fatalf("want 400 got %d", rr.Code)
	}
}

func TestMedia_SingleflightConcurrentRefetch(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("BYTES"))
	}))
	defer upstream.Close()

	h, repo, _ := newHandler(t)
	h.http = upstream.Client()
	url := upstream.URL + "/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 5, Status: media.StatusStored})
	r := newRouter(h)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
			if rr.Code != 200 {
				t.Errorf("concurrent: want 200 got %d", rr.Code)
			}
		}()
	}
	wg.Wait()
	if got := upstreamCalls.Load(); got > 2 {
		// Allow up to 2 because LRU population race between concurrent
		// requests can let a second one in before the singleflight closure
		// completes. >2 is the real failure.
		t.Fatalf("singleflight failed: %d upstream calls", got)
	}
}
