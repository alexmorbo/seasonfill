package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
)

// findLogLine returns the first newline-split line of out that contains
// every needle. Fails the test if no line matches — used by the W16-4
// level-assertion tests to pin a specific media.fetch.* JSON record.
func findLogLine(t *testing.T, out string, needles ...string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		ok := true
		for _, n := range needles {
			if !strings.Contains(line, n) {
				ok = false
				break
			}
		}
		if ok {
			return line
		}
	}
	t.Fatalf("no log line contains all of %v\nGot:\n%s", needles, out)
	return ""
}

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
	// Story 346: the default CDN cap moved from 5 rps to 100 rps. This
	// test explicitly pins 5 rps so the timing assertion stays
	// meaningful regardless of future default changes.
	//
	// W19-1: pin Workers:3 (was the pre-W19-1 default) so this timing
	// assertion stays valid. The test observes pending-row creation, and
	// handle() writes the pending row BEFORE hitting the limiter — so
	// with the new default of 32 workers >= 10 jobs, all rows land up
	// front and the paced download happens afterward. Pinning 3 workers
	// (< 10 jobs) restores the channel back-pressure that makes row
	// creation itself observe the 5 rps cap.
	d, err := NewDownloader(eq, DownloaderDeps{
		Store: newFakeStore(), Repo: repo,
		HTTPClient:      srv.Client(),
		Logger:          slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Workers:         3,
		CDNRateLimitRPS: 5,
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
	for i := range 10 {
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

// --- Story 312: structured logging tests ---

// syncBuffer is a goroutine-safe wrapper around bytes.Buffer so the slog
// handler can write concurrently while the test inspects the captured output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForLog(t *testing.T, buf *syncBuffer, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), needle) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log line %q\nGot:\n%s", needle, buf.String())
}

func TestDownloader_LogsFetchOk_OnSuccess(t *testing.T) {
	t.Parallel()
	buf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("img"))
	}))
	defer ts.Close()
	store := newFakeStore()
	repo := newFakeRepo()
	eq := NewEnqueuer(logger)
	dl, err := NewDownloader(eq, DownloaderDeps{Store: store, Repo: repo, HTTPClient: ts.Client(), Logger: logger})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	before := metrics.GetOrCreateCounter(`seasonfill_media_fetch_total{result="ok",error_kind=""}`).Get()
	dl.Start(ctx)
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: ts.URL + "/poster.jpg", Kind: "poster_w342", Extension: "jpg"}})
	waitForLog(t, buf, `"msg":"media.fetch.ok"`, 3*time.Second)
	eq.Close()
	dl.Close()
	out := buf.String()
	require.Contains(t, out, `"msg":"media.fetch.start"`)
	require.Contains(t, out, `"msg":"media.fetch.ok"`)
	require.Contains(t, out, `"content_type":"image/jpeg"`)
	require.Contains(t, out, `"kind":"poster_w342"`)
	require.Contains(t, out, `"size_bytes":3`)
	// W16-4: strict delta — the global VM registry is shared across
	// -parallel tests, so assert after > before (never an absolute).
	require.Greater(t, metrics.GetOrCreateCounter(`seasonfill_media_fetch_total{result="ok",error_kind=""}`).Get(), before)
}

func TestDownloader_LogsFetchFailed_OnHTTP500(t *testing.T) {
	t.Parallel()
	buf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()
	store := newFakeStore()
	repo := newFakeRepo()
	eq := NewEnqueuer(logger)
	dl, err := NewDownloader(eq, DownloaderDeps{Store: store, Repo: repo, HTTPClient: ts.Client(), Logger: logger})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	before := metrics.GetOrCreateCounter(`seasonfill_media_fetch_total{result="failed",error_kind="http_5xx"}`).Get()
	dl.Start(ctx)
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: ts.URL + "/poster.jpg", Kind: "poster_w342", Extension: "jpg"}})
	waitForLog(t, buf, `"msg":"media.fetch.failed"`, 5*time.Second)
	eq.Close()
	dl.Close()
	out := buf.String()
	require.Contains(t, out, `"msg":"media.fetch.failed"`)
	require.Contains(t, out, `"error_kind":"http_5xx"`)
	require.Contains(t, out, `"http_status":500`)
	// W16-4: transient (http_5xx) stays WARN — only s3_write_error escalates.
	line := findLogLine(t, out, `"msg":"media.fetch.failed"`, `"error_kind":"http_5xx"`)
	require.Contains(t, line, `"level":"WARN"`)
	require.Greater(t, metrics.GetOrCreateCounter(`seasonfill_media_fetch_total{result="failed",error_kind="http_5xx"}`).Get(), before)
}

func TestDownloader_LogsFetchFailed_OnHTTP404(t *testing.T) {
	t.Parallel()
	buf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer ts.Close()
	store := newFakeStore()
	repo := newFakeRepo()
	eq := NewEnqueuer(logger)
	dl, err := NewDownloader(eq, DownloaderDeps{Store: store, Repo: repo, HTTPClient: ts.Client(), Logger: logger})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dl.Start(ctx)
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: ts.URL + "/poster.jpg", Kind: "poster_w342", Extension: "jpg"}})
	waitForLog(t, buf, `"msg":"media.fetch.failed"`, 3*time.Second)
	eq.Close()
	dl.Close()
	out := buf.String()
	require.Contains(t, out, `"msg":"media.fetch.failed"`)
	require.Contains(t, out, `"error_kind":"http_4xx"`)
	require.Contains(t, out, `"http_status":404`)
}

// failingPutStore is a Store that returns an error on Put — drives the
// s3_write_error path.
type failingPutStore struct{ *fakeStore }

func (f failingPutStore) Put(_ context.Context, _ string, _ io.Reader, _ int64, _ string) error {
	return errors.New("S3 access denied")
}

func TestDownloader_LogsFetchFailed_OnStorePutFailure(t *testing.T) {
	t.Parallel()
	buf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("img"))
	}))
	defer ts.Close()
	store := failingPutStore{fakeStore: newFakeStore()}
	repo := newFakeRepo()
	eq := NewEnqueuer(logger)
	dl, err := NewDownloader(eq, DownloaderDeps{Store: store, Repo: repo, HTTPClient: ts.Client(), Logger: logger})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	before := metrics.GetOrCreateCounter(`seasonfill_media_fetch_total{result="failed",error_kind="s3_write_error"}`).Get()
	dl.Start(ctx)
	eq.Enqueue(ctx, []EnqueueRequest{{UpstreamURL: ts.URL + "/poster.jpg", Kind: "poster_w342", Extension: "jpg"}})
	waitForLog(t, buf, `"error_kind":"s3_write_error"`, 3*time.Second)
	eq.Close()
	dl.Close()
	out := buf.String()
	require.Contains(t, out, `"msg":"media.fetch.failed"`)
	require.Contains(t, out, `"error_kind":"s3_write_error"`)
	// W16-4: s3_write_error is the SeaweedFS-capacity signal → ERROR level.
	line := findLogLine(t, out, `"msg":"media.fetch.failed"`, `"error_kind":"s3_write_error"`)
	require.Contains(t, line, `"level":"ERROR"`)
	require.Greater(t, metrics.GetOrCreateCounter(`seasonfill_media_fetch_total{result="failed",error_kind="s3_write_error"}`).Get(), before)
}

// W19-1 (a) — default worker count is 32; DownloaderDeps.Workers overrides.
func TestDownloader_WorkerCount(t *testing.T) {
	newDL := func(workers int) *Downloader {
		eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		d, err := NewDownloader(eq, DownloaderDeps{
			Store:      newFakeStore(),
			Repo:       newFakeRepo(),
			HTTPClient: &http.Client{},
			Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Workers:    workers,
		})
		require.NoError(t, err)
		return d
	}

	// Unset → default 32.
	require.Equal(t, 32, newDL(0).workers, "unset Workers must default to 32")
	// Negative → default 32 (defensive).
	require.Equal(t, 32, newDL(-5).workers, "negative Workers must fall back to default")
	// Explicit override honoured.
	require.Equal(t, 8, newDL(8).workers, "explicit Workers must be honoured")
}

// W19-1 (a) — Start actually spawns d.workers goroutines. We count drain
// goroutines by feeding exactly N never-completing jobs (server blocks
// until the test ends) and asserting all N slots become busy at once.
func TestDownloader_SpawnsWorkerGoroutines(t *testing.T) {
	const n = 6
	var inFlight atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-release // hold the worker until the test releases
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	defer close(release)

	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := NewDownloader(eq, DownloaderDeps{
		Store: newFakeStore(), Repo: newFakeRepo(),
		HTTPClient: srv.Client(),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Workers:    n,
	})
	require.NoError(t, err)
	ctx := t.Context()
	d.Start(ctx)

	reqs := make([]EnqueueRequest, n+2) // more jobs than workers
	for i := range reqs {
		reqs[i] = EnqueueRequest{UpstreamURL: srv.URL + "/img" + strconv.Itoa(i) + ".jpg", Kind: "poster_w342", Extension: "jpg"}
	}
	eq.Enqueue(ctx, reqs)

	// Exactly n workers should be simultaneously blocked in the handler.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inFlight.Load() == int32(n) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, int32(n), inFlight.Load(), "exactly n=%d workers must be draining concurrently", n)
}

// W19-1 (b) — unset CDNRateLimitRPS → UNCAPPED (rate.Inf). Replaces the
// pre-W19-1 TestDownloader_DefaultCDNRate100 (which expected 100).
func TestDownloader_DefaultCDNUnlimited(t *testing.T) {
	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := NewDownloader(eq, DownloaderDeps{
		Store:      newFakeStore(),
		Repo:       newFakeRepo(),
		HTTPClient: &http.Client{},
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		// CDNRateLimitRPS left at zero — expect uncapped.
	})
	require.NoError(t, err)
	require.Equal(t, rate.Inf, d.Limiter().Limit(), "unset CDN rate must be uncapped (rate.Inf)")

	// rate.Inf never blocks: many Allow() calls succeed instantly.
	for range 1000 {
		require.True(t, d.Limiter().Allow(), "rate.Inf limiter must never throttle")
	}
}

// W19-1 (b) — negative CDNRateLimitRPS also collapses to uncapped.
// Replaces the pre-W19-1 TestDownloader_NegativeRateFallsBack (expected 100).
func TestDownloader_NegativeRateUnlimited(t *testing.T) {
	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := NewDownloader(eq, DownloaderDeps{
		Store:           newFakeStore(),
		Repo:            newFakeRepo(),
		HTTPClient:      &http.Client{},
		Logger:          slog.New(slog.NewJSONHandler(io.Discard, nil)),
		CDNRateLimitRPS: -1,
	})
	require.NoError(t, err)
	require.Equal(t, rate.Inf, d.Limiter().Limit(), "negative CDN rate must be uncapped (rate.Inf)")
}

// W19-1 (b) — a positive CDNRateLimitRPS still imposes a finite cap
// (rollback path). Keeps burst=1 so steady pacing is exactly rps.
func TestDownloader_PositiveRateFiniteCap(t *testing.T) {
	eq := NewEnqueuer(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := NewDownloader(eq, DownloaderDeps{
		Store:           newFakeStore(),
		Repo:            newFakeRepo(),
		HTTPClient:      &http.Client{},
		Logger:          slog.New(slog.NewJSONHandler(io.Discard, nil)),
		CDNRateLimitRPS: 25,
	})
	require.NoError(t, err)
	require.InDelta(t, 25.0, float64(d.Limiter().Limit()), 0.001, "explicit finite CDN cap must be honoured")
	require.NotEqual(t, rate.Inf, d.Limiter().Limit())
}
