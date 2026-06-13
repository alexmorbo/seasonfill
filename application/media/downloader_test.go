package media

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/domain/media"
	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
)

// fakeRepo is a thread-safe in-memory AssetRepo.
type fakeRepo struct {
	mu     sync.Mutex
	byHash map[string]media.Asset
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byHash: map[string]media.Asset{}} }

func (r *fakeRepo) Get(ctx context.Context, hash string) (media.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byHash[hash]
	if !ok {
		return media.Asset{}, ErrAssetNotFound
	}
	return a, nil
}

func (r *fakeRepo) Upsert(ctx context.Context, a media.Asset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byHash[a.Hash] = a
	return nil
}

// fakeStore is an in-memory mediastore.Store.
type fakeStore struct {
	mu    sync.Mutex
	bytes map[string][]byte
	cts   map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{bytes: map[string][]byte{}, cts: map[string]string{}}
}

func (s *fakeStore) Get(ctx context.Context, key string) (io.ReadCloser, mediastore.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bytes[key]
	if !ok {
		return nil, mediastore.ObjectInfo{}, mediastore.ErrNotFound
	}
	return io.NopCloser(bytesReader(b)), mediastore.ObjectInfo{Key: key, Size: int64(len(b)), ContentType: s.cts[key]}, nil
}

func (s *fakeStore) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytes[key] = body
	s.cts[key] = ct
	return nil
}

func (s *fakeStore) Stat(ctx context.Context, key string) (mediastore.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bytes[key]
	if !ok {
		return mediastore.ObjectInfo{}, mediastore.ErrNotFound
	}
	return mediastore.ObjectInfo{Key: key, Size: int64(len(b)), ContentType: s.cts[key]}, nil
}

func (s *fakeStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bytes, key)
	delete(s.cts, key)
	return nil
}

func (s *fakeStore) List(ctx context.Context, prefix string, fn func(mediastore.ObjectInfo) error) error {
	return nil
}

func TestDownloader_RetryThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("OK-BYTES"))
	}))
	defer srv.Close()

	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	repo := newFakeRepo()
	store := newFakeStore()
	d, err := NewDownloader(eq, DownloaderDeps{
		Store: store, Repo: repo,
		HTTPClient: srv.Client(),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)

	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: srv.URL + "/abc.jpg", Kind: "poster_w342", Extension: "jpg"}})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if a, err := repo.Get(ctx, HashFromURL(srv.URL+"/abc.jpg")); err == nil && a.Status == media.StatusStored {
			cancel()
			eq.Close()
			d.Close()
			if calls.Load() != 2 {
				t.Fatalf("want 2 calls (1 fail + 1 retry), got %d", calls.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for status=stored")
}

func TestDownloader_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	repo := newFakeRepo()
	d, err := NewDownloader(eq, DownloaderDeps{
		Store: newFakeStore(), Repo: repo,
		HTTPClient: srv.Client(),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)

	// 10 requests at 5 rps must take at least ~1.2s (lower bound is
	// loose — Limiter burst=1 with 3 workers permits the first 3
	// requests to fire near-instantaneously; the remaining 7 pace at
	// 200ms apart minimum).
	urls := make([]EnqueueRequest, 10)
	for i := 0; i < 10; i++ {
		urls[i] = EnqueueRequest{UpstreamURL: srv.URL + "/img" + strconv.Itoa(i) + ".jpg", Kind: "poster_w342", Extension: "jpg"}
	}
	start := time.Now()
	eq.Enqueue(ctx, urls)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		n := len(repo.byHash)
		repo.mu.Unlock()
		if n == 10 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(start)
	cancel()
	eq.Close()
	d.Close()
	// Lower bound: (10 - burst) / rate = (10 - 1) / 5 = 1.8s. Allow
	// 600ms slack for the burst + worker-startup scheduling jitter.
	const minElapsed = 1200 * time.Millisecond
	if elapsed < minElapsed {
		t.Fatalf("rate limit too loose: 10 reqs at 5 rps took only %v (want >= %v)", elapsed, minElapsed)
	}
}

func TestDownloader_StatHitSkipsUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("upstream must not be hit on Stat-hit")
	}))
	defer srv.Close()
	url := srv.URL + "/abc.jpg"
	key := mediastore.Key(url, "jpg")

	store := newFakeStore()
	_ = store.Put(context.Background(), key, bytesReader([]byte("ALREADY")), 7, "image/jpeg")

	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	repo := newFakeRepo()
	d, _ := NewDownloader(eq, DownloaderDeps{
		Store: store, Repo: repo,
		HTTPClient: srv.Client(),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: url, Kind: "poster_w342", Extension: "jpg"}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a, err := repo.Get(ctx, HashFromURL(url)); err == nil && a.Status == media.StatusStored {
			cancel()
			eq.Close()
			d.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestDownloader_AllFailsMarksFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	repo := newFakeRepo()
	d, _ := NewDownloader(eq, DownloaderDeps{
		Store: newFakeStore(), Repo: repo,
		HTTPClient: srv.Client(),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	url := srv.URL + "/abc.jpg"
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: url, Kind: "poster_w342", Extension: "jpg"}})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if a, err := repo.Get(ctx, HashFromURL(url)); err == nil && a.Status == media.StatusFailed {
			cancel()
			eq.Close()
			d.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out — expected status=failed")
}
