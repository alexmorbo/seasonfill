package media

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
)

// ondemandFakeStore implements mediastore.Store for ondemand tests.
// Renamed from the suggested "fakeStore" so it doesn't collide with
// the downloader_test.go fake in the same package.
type ondemandFakeStore struct {
	puts map[string][]byte
	cts  map[string]string
}

func newOndemandFakeStore() *ondemandFakeStore {
	return &ondemandFakeStore{puts: map[string][]byte{}, cts: map[string]string{}}
}

func (s *ondemandFakeStore) Stat(_ context.Context, key string) (mediastore.ObjectInfo, error) {
	if b, ok := s.puts[key]; ok {
		return mediastore.ObjectInfo{Size: int64(len(b)), ContentType: s.cts[key]}, nil
	}
	return mediastore.ObjectInfo{}, mediastore.ErrNotFound
}

func (s *ondemandFakeStore) Get(_ context.Context, key string) (io.ReadCloser, mediastore.ObjectInfo, error) {
	if b, ok := s.puts[key]; ok {
		return io.NopCloser(bytes.NewReader(b)), mediastore.ObjectInfo{Size: int64(len(b)), ContentType: s.cts[key]}, nil
	}
	return nil, mediastore.ObjectInfo{}, mediastore.ErrNotFound
}

func (s *ondemandFakeStore) Put(_ context.Context, key string, r io.Reader, _ int64, ct string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.puts[key] = b
	s.cts[key] = ct
	return nil
}

func (s *ondemandFakeStore) Delete(_ context.Context, key string) error {
	delete(s.puts, key)
	return nil
}

func (s *ondemandFakeStore) List(_ context.Context, _ string, _ func(mediastore.ObjectInfo) error) error {
	return nil
}

// ondemandFakeRepo implements AssetRepo for ondemand tests.
type ondemandFakeRepo struct {
	rows map[string]media.Asset
}

func newOndemandFakeRepo() *ondemandFakeRepo {
	return &ondemandFakeRepo{rows: map[string]media.Asset{}}
}

func (r *ondemandFakeRepo) Get(_ context.Context, hash string) (media.Asset, error) {
	if a, ok := r.rows[hash]; ok {
		return a, nil
	}
	return media.Asset{}, ErrAssetNotFound
}

func (r *ondemandFakeRepo) Upsert(_ context.Context, a media.Asset) error {
	r.rows[a.Hash] = a
	return nil
}

func TestOnDemandFetcher_FetchSync_HappyPath(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()

	store := newOndemandFakeStore()
	repo := newOndemandFakeRepo()
	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: store, Repo: repo,
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1), // no throttle for tests
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	hash, ok := f.FetchSync(t.Context(), server.URL+"/img.jpg", "poster_w342", "jpg")
	require.True(t, ok, "FetchSync should succeed")
	assert.Len(t, hash, 64, "hash should be 64-char sha256 hex")
	assert.Equal(t, media.StatusStored, repo.rows[hash].Status)
	assert.Equal(t, []byte("hello"), store.puts[mediastore.Key(server.URL+"/img.jpg", "jpg")])
}

func TestOnDemandFetcher_FetchSync_StatHitShortCircuit(t *testing.T) {
	t.Parallel()
	store := newOndemandFakeStore()
	repo := newOndemandFakeRepo()
	// Pre-seed the store. Caller URL — the key is derived deterministically.
	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	store.puts[mediastore.Key(url, "jpg")] = []byte("preexist")
	store.cts[mediastore.Key(url, "jpg")] = "image/jpeg"

	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: store, Repo: repo,
		Limiter: rate.NewLimiter(rate.Inf, 1),
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	hash, ok := f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.True(t, ok)
	assert.Equal(t, HashFromURL(url), hash)
	assert.Equal(t, media.StatusStored, repo.rows[hash].Status)
}

func TestOnDemandFetcher_FetchSync_Timeout(t *testing.T) {
	t.Parallel()
	// Server hangs until the client cancels the request.
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond},
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, ok := f.FetchSync(ctx, server.URL+"/slow.jpg", "poster_w342", "jpg")
	assert.False(t, ok, "FetchSync should fail on timeout")
}

func TestOnDemandFetcher_FetchSync_HTTPError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	_, ok := f.FetchSync(t.Context(), server.URL+"/missing.jpg", "poster_w342", "jpg")
	assert.False(t, ok)
}

// TestOnDemandFetcher_FetchSync_NegativeCache_SkipsWithinTTL — W16-5: a
// FAILING fetch puts the hash into an in-memory cooldown; a second FetchSync
// for the same URL within negativeCacheTTL is skipped (no second round trip
// to the upstream), protecting TMDB from per-render re-hammering.
func TestOnDemandFetcher_FetchSync_NegativeCache_SkipsWithinTTL(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	now := time.Now().UTC()
	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Clock:      func() time.Time { return now },
	})
	require.NoError(t, err)

	url := server.URL + "/img.jpg"
	_, ok := f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.False(t, ok, "first fetch fails")
	require.Equal(t, int32(1), hits.Load())

	// Same URL, clock not advanced → cooldown skips the round trip.
	_, ok = f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.False(t, ok, "second fetch stays failed")
	assert.Equal(t, int32(1), hits.Load(), "second call must be skipped by cooldown")
}

// TestOnDemandFetcher_FetchSync_NegativeCache_RetriesAfterTTL — the cooldown
// self-heals: once negativeCacheTTL elapses, the next FetchSync retries the
// upstream (round-trip counter advances).
func TestOnDemandFetcher_FetchSync_NegativeCache_RetriesAfterTTL(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	now := time.Now().UTC()
	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Clock:      func() time.Time { return now },
	})
	require.NoError(t, err)

	url := server.URL + "/img.jpg"
	_, ok := f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.False(t, ok)
	require.Equal(t, int32(1), hits.Load())

	// Advance past the TTL → cooldown expires → retry hits upstream again.
	now = now.Add(negativeCacheTTL + time.Second)
	_, ok = f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.False(t, ok)
	assert.Equal(t, int32(2), hits.Load(), "post-TTL call must retry the upstream")
}

// TestOnDemandFetcher_FetchSync_Success_NoCooldown — negative case: a
// SUCCESSFUL fetch must NOT leave the hash in cooldown (heals immediately).
func TestOnDemandFetcher_FetchSync_Success_NoCooldown(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()

	f, err := NewOnDemandFetcher(OnDemandDeps{
		Store: newOndemandFakeStore(), Repo: newOndemandFakeRepo(),
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	url := server.URL + "/img.jpg"
	hash, ok := f.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.True(t, ok)

	impl, isImpl := f.(*onDemandFetcher)
	require.True(t, isImpl, "expected concrete *onDemandFetcher")
	assert.False(t, impl.inCooldown(hash), "successful fetch must not leave a cooldown entry")
}

// TestOnDemandFetcher_FetchSync_NegativeCache_HealsOnRecovery — W16-5: once a
// failed hash is in cooldown, an asset that becomes available in the store
// (e.g. the async Downloader filled it) heals IMMEDIATELY via the stat
// short-circuit — which sits BEFORE the cooldown gate — clearing the cooldown
// WITHOUT waiting out the TTL and WITHOUT a fresh upstream round trip. Clock is
// never advanced, so this isolates clearCooldown from mere TTL expiry.
func TestOnDemandFetcher_FetchSync_NegativeCache_HealsOnRecovery(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	store := newOndemandFakeStore()
	now := time.Now().UTC()
	fi, err := NewOnDemandFetcher(OnDemandDeps{
		Store: store, Repo: newOndemandFakeRepo(),
		HTTPClient: server.Client(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Clock:      func() time.Time { return now },
	})
	require.NoError(t, err)
	impl, isImpl := fi.(*onDemandFetcher)
	require.True(t, isImpl, "expected concrete *onDemandFetcher")

	url := server.URL + "/img.jpg"
	hash := HashFromURL(url)

	// Call 1: store miss → http fails → cooldown set.
	_, ok := fi.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.False(t, ok)
	require.Equal(t, int32(1), hits.Load())
	require.True(t, impl.inCooldown(hash), "failed fetch must set cooldown")

	// Simulate the async Downloader having filled the store.
	key := mediastore.Key(url, "jpg")
	store.puts[key] = []byte("hello")
	store.cts[key] = "image/jpeg"

	// Call 2 (clock NOT advanced): stat short-circuit serves+clears BEFORE the
	// cooldown gate — no new round trip, cooldown gone.
	got, ok := fi.FetchSync(t.Context(), url, "poster_w342", "jpg")
	require.True(t, ok, "stored asset must serve despite live cooldown")
	require.Equal(t, hash, got)
	assert.Equal(t, int32(1), hits.Load(), "stat hit must not re-hit upstream")
	assert.False(t, impl.inCooldown(hash), "stat-hit path must clear the cooldown")
}

func TestOnDemandFetcher_Nil(t *testing.T) {
	t.Parallel()
	_, err := NewOnDemandFetcher(OnDemandDeps{}) // empty
	require.Error(t, err)
}
