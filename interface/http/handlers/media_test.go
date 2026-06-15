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
	"time"

	"github.com/gin-gonic/gin"

	appmedia "github.com/alexmorbo/seasonfill/application/media"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/media"
	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
)

// stubRepo for the handler tests.
type stubRepo struct {
	mu       sync.Mutex
	byHash   map[string]media.Asset
	getCalls atomic.Int32
}

func newStubRepo() *stubRepo { return &stubRepo{byHash: map[string]media.Asset{}} }

func (s *stubRepo) put(a media.Asset) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[a.Hash] = a
}

func (s *stubRepo) Get(ctx context.Context, hash string) (media.Asset, error) {
	s.getCalls.Add(1)
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
	h := NewMediaHandler(MediaHandlerDeps{
		Store:      store,
		Repo:       repo,
		HTTPClient: http.DefaultClient,
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return h, repo, store
}

func newRouter(h *MediaHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/media/:hash", h.Serve)
	// Mirror server.go: HEAD shares the GET handler so probes (curl -I,
	// CDN warmup, monitoring) get a 200+headers reply instead of the
	// default Gin no-route 404.
	r.HEAD("/api/v1/media/:hash", h.Serve)
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

// Story 321: pending without fetcher/resolver wiring → SVG placeholder
// (the wiring is nil-OK; main.go late-binds it after enrichBundle).
func TestMedia_PendingServesPlaceholderWhenUnwired(t *testing.T) {
	h, repo, _ := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusPending})
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 (placeholder) got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Fatalf("want image/svg+xml Content-Type, got %q", got)
	}
	if rr.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatal("expected X-Media-Placeholder=1")
	}
	if !strings.Contains(rr.Body.String(), "<svg") {
		t.Fatalf("expected SVG body, got %q", rr.Body.String()[:min(120, rr.Body.Len())])
	}
}

// Story 321: failed rows short-circuit to placeholder (negative cache).
func TestMedia_FailedServesPlaceholder(t *testing.T) {
	h, repo, _ := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusFailed})
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 (placeholder) got %d", rr.Code)
	}
	if rr.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatal("expected X-Media-Placeholder=1")
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

// --- Story 321 on-demand fetch tests ------------------------------------

// stubPendingResolver satisfies MediaPendingResolver.
type stubPendingResolver struct {
	mu   sync.Mutex
	rows map[string]pendingRow
	err  error
}

type pendingRow struct {
	source string
	kind   string
	status media.Status
}

func newStubPendingResolver() *stubPendingResolver {
	return &stubPendingResolver{rows: map[string]pendingRow{}}
}

func (s *stubPendingResolver) put(hash, source, kind string, status media.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[hash] = pendingRow{source: source, kind: kind, status: status}
}

func (s *stubPendingResolver) GetSourceURLByHash(_ context.Context, hash string) (string, string, media.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", "", "", s.err
	}
	r, ok := s.rows[hash]
	if !ok {
		return "", "", "", ports.ErrNotFound
	}
	return r.source, r.kind, r.status, nil
}

// stubOnDemand mirrors application/media.OnDemandFetcher for the
// handler tests. When hashWin is "" the fetcher always misses.
type stubOnDemand struct {
	mu       sync.Mutex
	calls    int32
	hashWin  string
	bytes    []byte
	contentT string
	store    mediastore.Store
	repo     MediaAssetReader
	delay    time.Duration
}

func (f *stubOnDemand) FetchSync(ctx context.Context, sourceURL, kind, ext string) (string, bool) {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", false
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hashWin == "" {
		return "", false
	}
	if f.store != nil {
		_ = f.store.Put(ctx, mediastore.Key(sourceURL, ext), bytes.NewReader(f.bytes), int64(len(f.bytes)), f.contentT)
	}
	if f.repo != nil {
		_ = f.repo.Upsert(ctx, media.Asset{
			Hash: f.hashWin, UpstreamURL: sourceURL, Kind: kind,
			ContentType: f.contentT, Size: int64(len(f.bytes)),
			Status: media.StatusStored,
		})
	}
	return f.hashWin, true
}

func newOnDemandHandler(t *testing.T, resolver MediaPendingResolver, fetcher MediaOnDemandSyncFetcher) (*MediaHandler, *stubRepo, *stubStore) {
	t.Helper()
	repo := newStubRepo()
	store := newStubStore()
	h := NewMediaHandler(MediaHandlerDeps{
		Store:           store,
		Repo:            repo,
		PendingResolver: resolver,
		OnDemandFetcher: fetcher,
		Logger:          slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return h, repo, store
}

func TestMediaHandler_OnDemand_PendingHitFillsAndServes(t *testing.T) {
	url := "https://image.tmdb.org/t/p/w342/abc-ondemand.jpg"
	hash := hashOf(url)
	resolver := newStubPendingResolver()
	resolver.put(hash, url, "poster_w342", media.StatusPending)
	fetcher := &stubOnDemand{hashWin: hash, bytes: []byte("PNG"), contentT: "image/jpeg"}
	h, repo, store := newOnDemandHandler(t, resolver, fetcher)
	fetcher.store = store
	fetcher.repo = repo
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusPending})

	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "PNG" {
		t.Fatalf("want PNG body, got %q", rr.Body.String())
	}
	if got := atomic.LoadInt32(&fetcher.calls); got != 1 {
		t.Fatalf("want 1 fetcher call, got %d", got)
	}
	if rr.Header().Get("X-Media-Placeholder") != "" {
		t.Fatal("must NOT serve placeholder on success path")
	}
}

func TestMediaHandler_OnDemand_PendingMissServesPlaceholderAndStampsFailed(t *testing.T) {
	url := "https://image.tmdb.org/t/p/w1280/xx-miss.jpg"
	hash := hashOf(url)
	resolver := newStubPendingResolver()
	resolver.put(hash, url, "backdrop_w1280", media.StatusPending)
	fetcher := &stubOnDemand{hashWin: ""}
	h, repo, _ := newOnDemandHandler(t, resolver, fetcher)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "backdrop_w1280", Status: media.StatusPending})

	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 (placeholder) got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Fatalf("want image/svg+xml Content-Type, got %q", ct)
	}
	if rr.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatal("expected X-Media-Placeholder=1")
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "public, max-age=300" {
		t.Fatalf("want 5-min cache-control, got %q", cc)
	}
	if !strings.Contains(rr.Body.String(), "<svg") {
		t.Fatal("body must be SVG")
	}
	if got := atomic.LoadInt32(&fetcher.calls); got != 1 {
		t.Fatalf("want 1 fetcher call, got %d", got)
	}
	// Row stamped failed.
	row, err := repo.Get(t.Context(), hash)
	if err != nil {
		t.Fatalf("repo.Get after miss: %v", err)
	}
	if row.Status != media.StatusFailed {
		t.Fatalf("want status=failed after miss, got %s", row.Status)
	}
}

func TestMediaHandler_OnDemand_FailedShortCircuits(t *testing.T) {
	url := "https://image.tmdb.org/t/p/w342/failed.jpg"
	hash := hashOf(url)
	resolver := newStubPendingResolver()
	fetcher := &stubOnDemand{hashWin: hash, bytes: []byte("Y"), contentT: "image/jpeg"}
	h, repo, _ := newOnDemandHandler(t, resolver, fetcher)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusFailed})

	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 (placeholder) got %d", rr.Code)
	}
	if rr.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatal("expected placeholder header")
	}
	if got := atomic.LoadInt32(&fetcher.calls); got != 0 {
		t.Fatalf("failed rows must NOT trigger TMDB call, got %d", got)
	}
}

func TestMediaHandler_OnDemand_SingleflightDedupes(t *testing.T) {
	url := "https://image.tmdb.org/t/p/w342/sf-dedupe.jpg"
	hash := hashOf(url)
	resolver := newStubPendingResolver()
	resolver.put(hash, url, "poster_w342", media.StatusPending)
	fetcher := &stubOnDemand{
		hashWin: hash, bytes: []byte("OK"), contentT: "image/jpeg",
		delay: 200 * time.Millisecond,
	}
	h, repo, store := newOnDemandHandler(t, resolver, fetcher)
	fetcher.store = store
	fetcher.repo = repo
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusPending})

	r := newRouter(h)

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&fetcher.calls); got != 1 {
		t.Fatalf("singleflight must collapse to one TMDB call, got %d", got)
	}
}

func TestMediaHandler_OnDemand_NilFetcherServesPlaceholder(t *testing.T) {
	url := "https://image.tmdb.org/t/p/w342/legacy.jpg"
	hash := hashOf(url)
	h, repo, _ := newHandler(t) // no fetcher, no resolver
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusPending})
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 (placeholder) got %d", rr.Code)
	}
	if rr.Header().Get("X-Media-Placeholder") != "1" {
		t.Fatal("expected placeholder header")
	}
}

// TestMediaHandler_Placeholder_ContentAndHeaders confirms placeholder
// response shape end-to-end (operator override of story 321 404 path).
func TestMediaHandler_Placeholder_ContentAndHeaders(t *testing.T) {
	url := "https://image.tmdb.org/t/p/w342/placeholder-shape.jpg"
	hash := hashOf(url)
	resolver := newStubPendingResolver()
	resolver.put(hash, url, "poster_w342", media.StatusPending)
	fetcher := &stubOnDemand{hashWin: ""} // miss
	h, repo, _ := newOnDemandHandler(t, resolver, fetcher)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusPending})

	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Errorf("Content-Type want image/svg+xml, got %q", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control want 5min, got %q", got)
	}
	if got := rr.Header().Get("X-Media-Placeholder"); got != "1" {
		t.Errorf("X-Media-Placeholder want 1, got %q", got)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<svg") || !strings.Contains(body, "no image") {
		t.Errorf("body must be the no-image SVG, got %q", body[:min(200, len(body))])
	}
}

// Story 347 — sentinel served as the SVG placeholder without a DB call.
func TestMedia_SentinelServesSVG_NoDBCall(t *testing.T) {
	h, repo, store := newHandler(t)
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/media/"+appmedia.SentinelMissingHash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Fatalf("want image/svg+xml, got %q", got)
	}
	if got := rr.Header().Get("X-Media-Placeholder"); got != "sentinel" {
		t.Fatalf("X-Media-Placeholder want 'sentinel', got %q", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=86400" {
		t.Fatalf("Cache-Control want public,max-age=86400, got %q", got)
	}
	if !strings.Contains(rr.Body.String(), "<svg") {
		t.Fatalf("body must be SVG, got %q", rr.Body.String()[:min(120, rr.Body.Len())])
	}
	if got := repo.getCalls.Load(); got != 0 {
		t.Fatalf("sentinel path must not call repo.Get; got %d calls", got)
	}
	if got := store.calls.Load(); got != 0 {
		t.Fatalf("sentinel path must not call store.Get; got %d calls", got)
	}
}

// Story 347 — normal hashes still flow through the legacy path.
func TestMedia_NonSentinelHash_StillReadsRepo(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/normal.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, "jpg"), bytes.NewReader([]byte("PNG")), 3, "image/jpeg")
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if repo.getCalls.Load() == 0 {
		t.Fatal("normal hash must reach repo.Get")
	}
}

// Regression — HEAD on the media route MUST hit the same handler the
// GET path uses instead of falling through to Gin's default no-route
// 404. Production curl -I, CDN warm-ups, and uptime probes hit HEAD,
// and pre-fix they got a default 18-byte "404 page not found" because
// only GET was registered. The handler writes the same headers for
// both methods (Go's net/http server suppresses the body on HEAD); we
// assert the success code + identifying header.
func TestMedia_SentinelServesOnHEAD(t *testing.T) {
	h, _, _ := newHandler(t)
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodHead,
		"/api/v1/media/"+appmedia.SentinelMissingHash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("HEAD sentinel want 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("X-Media-Placeholder"); got != "sentinel" {
		t.Fatalf("X-Media-Placeholder want 'sentinel', got %q", got)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Fatalf("Content-Type want image/svg+xml, got %q", got)
	}
}

// Regression companion — HEAD on a normal stored hash returns 200 +
// the same ETag / Cache-Control as GET. Locks in the symmetric
// behavior promised by registering HEAD on the route.
func TestMedia_NormalHashServesOnHEAD(t *testing.T) {
	h, repo, store := newHandler(t)
	url := "https://image.tmdb.org/t/p/w342/normal-head.jpg"
	hash := hashOf(url)
	repo.put(media.Asset{Hash: hash, UpstreamURL: url, Kind: "poster_w342", ContentType: "image/jpeg", Size: 3, Status: media.StatusStored})
	_ = store.Put(context.Background(), mediastore.Key(url, "jpg"), bytes.NewReader([]byte("PNG")), 3, "image/jpeg")
	r := newRouter(h)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/api/v1/media/"+hash, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("HEAD normal want 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != `"`+hash+`"` {
		t.Fatalf("ETag want %q, got %q", `"`+hash+`"`, got)
	}
}
