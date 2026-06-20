package media

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestOnDemandFetcher_Nil(t *testing.T) {
	t.Parallel()
	_, err := NewOnDemandFetcher(OnDemandDeps{}) // empty
	require.Error(t, err)
}
