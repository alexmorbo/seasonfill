package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// stubTorrentsCachePort is a minimal SeriesCachePort for the handler.
type stubTorrentsCachePort struct {
	entry series.CacheEntry
	err   error
}

func (s stubTorrentsCachePort) Get(_ context.Context, _ string, _ int) (series.CacheEntry, error) {
	return s.entry, s.err
}

// stubTorrentsSeriesPort is a minimal SeriesPort for the handler.
type stubTorrentsSeriesPort struct {
	canon series.Canon
	err   error
}

func (s stubTorrentsSeriesPort) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	return s.canon, s.err
}

func (s stubTorrentsSeriesPort) GetByTMDBID(_ context.Context, _ int) (series.Canon, error) {
	return s.canon, s.err
}

// stubTorrentsLookup adapts a literal hash list to the LookupRepo port.
type stubTorrentsLookup struct {
	hashes []string
	err    error
}

func (s stubTorrentsLookup) HashesForSeries(_ context.Context, _ string, _ int) ([]string, error) {
	return s.hashes, s.err
}

// stubTorrentsRepo is the minimum TorrentsRepo surface the query
// needs in handler-scope tests.
type stubTorrentsRepo struct {
	byHash map[string]torrentsync.Entry
}

func (s stubTorrentsRepo) Upsert(_ context.Context, _ string, _ torrentsync.Entry) error {
	return nil
}

func (s stubTorrentsRepo) BatchUpsert(_ context.Context, _ string, _ []torrentsync.Entry, _ time.Time) error {
	return nil
}

func (s stubTorrentsRepo) MarkAbsent(_ context.Context, _ string, _ string, _ time.Time) error {
	return nil
}

func (s stubTorrentsRepo) List(_ context.Context, _ string) ([]torrentsync.Entry, error) {
	return nil, nil
}

func (s stubTorrentsRepo) FindByHashes(_ context.Context, _ string, hashes []string) ([]torrentsync.Entry, error) {
	out := make([]torrentsync.Entry, 0, len(hashes))
	for _, h := range hashes {
		if e, ok := s.byHash[h]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func i64ptrLocal(v int64) *domain.SeriesID { sid := domain.SeriesID(v); return &sid }

func buildTorrentsHandler(t *testing.T, store *torrentsync.Store, lookup stubTorrentsLookup, repo stubTorrentsRepo) *SeriesTorrentsHandler {
	t.Helper()
	q := torrentsync.NewQuery(store, repo, lookup).
		WithClock(func() time.Time { return time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) })
	return NewSeriesTorrentsHandler(q,
		stubTorrentsCachePort{entry: series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrLocal(42)}},
		stubTorrentsSeriesPort{canon: series.Canon{ID: 42, Title: "Test"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func TestSeriesTorrents_200_LiveAndDeadRendered(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	store := torrentsync.NewStore()
	store.EnsureInstance("alpha")
	five := 5
	store.Put("alpha", torrentsync.Entry{
		Info: qbit.TorrentInfo{
			Hash: "aaaa", Name: "live", StateRaw: "uploading",
			StateGroup:   qbit.StateGroupSeeding,
			DlSpeed:      1024,
			AddedOn:      time.Date(2026, 6, 13, 11, 0, 0, 0, time.UTC),
			SeasonNumber: &five,
		},
		StateGroup: qbit.StateGroupSeeding,
		SyncedAt:   time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
	})
	store.SetSeriesMapping("alpha", "aaaa", 1)

	repo := stubTorrentsRepo{
		byHash: map[string]torrentsync.Entry{
			"bbbb": {
				Info: qbit.TorrentInfo{
					Hash: "bbbb", Name: "dead",
					StateRaw: "stoppedUP", StateGroup: qbit.StateGroupPaused,
					DlSpeed: 9999, // MUST be zeroed
					AddedOn: time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC),
				},
				StateGroup: qbit.StateGroupPaused,
				SyncedAt:   time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC),
			},
		},
	}
	lookup := stubTorrentsLookup{hashes: []string{"aaaa", "bbbb"}}
	h := buildTorrentsHandler(t, store, lookup, repo)

	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/torrents", h.Get)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/torrents", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Header().Get("ETag"))
	var body dto.SeriesTorrentsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 2, body.TotalCount)
	require.Equal(t, 1, body.LiveCount)
	// First row — live, newer added_on.
	assert.Equal(t, "aaaa", body.Torrents[0].Hash)
	assert.True(t, body.Torrents[0].Live)
	assert.EqualValues(t, 1024, body.Torrents[0].DLSpeed)
	// Story 308: season_number must surface end-to-end through the
	// QueryRow → DTO mapping. The parsed season from qbit_torrents
	// (or the in-memory store's TorrentInfo) lands in the JSON.
	require.NotNil(t, body.Torrents[0].SeasonNumber, "season_number must propagate from TorrentInfo to wire")
	assert.Equal(t, 5, *body.Torrents[0].SeasonNumber)
	// Second row — dead, live cells zeroed.
	assert.Equal(t, "bbbb", body.Torrents[1].Hash)
	assert.False(t, body.Torrents[1].Live)
	assert.EqualValues(t, 0, body.Torrents[1].DLSpeed)
	// And the DB-only row whose TorrentInfo has nil SeasonNumber
	// must yield nil/absent on the wire.
	assert.Nil(t, body.Torrents[1].SeasonNumber, "nil SeasonNumber must surface as absent")
}

func TestSeriesTorrents_304_OnIfNoneMatch(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	store := torrentsync.NewStore()
	store.EnsureInstance("alpha")
	repo := stubTorrentsRepo{}
	lookup := stubTorrentsLookup{hashes: nil}
	h := buildTorrentsHandler(t, store, lookup, repo)

	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/torrents", h.Get)

	// First call — capture the ETag.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/torrents", nil)
	r.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)
	etag := rec1.Header().Get("ETag")
	require.NotEmpty(t, etag)

	// Second call — If-None-Match matches → 304.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/torrents", nil)
	req2.Header.Set("If-None-Match", etag)
	r.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusNotModified, rec2.Code)
	assert.Equal(t, etag, rec2.Header().Get("ETag"))
	assert.Empty(t, rec2.Body.String())
}

func TestSeriesTorrents_404_UnknownSeries(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	store := torrentsync.NewStore()
	repo := stubTorrentsRepo{}
	lookup := stubTorrentsLookup{}
	q := torrentsync.NewQuery(store, repo, lookup)
	h := NewSeriesTorrentsHandler(q,
		stubTorrentsCachePort{err: ports.ErrNotFound},
		stubTorrentsSeriesPort{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/torrents", h.Get)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/999/torrents", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSeriesTorrents_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	store := torrentsync.NewStore()
	repo := stubTorrentsRepo{}
	lookup := stubTorrentsLookup{}
	q := torrentsync.NewQuery(store, repo, lookup)
	h := NewSeriesTorrentsHandler(q, stubTorrentsCachePort{}, stubTorrentsSeriesPort{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/torrents", h.Get)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/abc/torrents", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSeriesTorrents_DefaultSortAddedOnDesc(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	store := torrentsync.NewStore()
	store.EnsureInstance("alpha")
	// Three live entries with descending added_on.
	for i, h := range []string{"old", "mid", "new"} {
		store.Put("alpha", torrentsync.Entry{
			Info: qbit.TorrentInfo{
				Hash:       h,
				Name:       h,
				StateRaw:   "uploading",
				StateGroup: qbit.StateGroupSeeding,
				AddedOn:    time.Date(2026, 6, 10+i, 0, 0, 0, 0, time.UTC),
			},
			StateGroup: qbit.StateGroupSeeding,
			SyncedAt:   time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		})
		store.SetSeriesMapping("alpha", h, 1)
	}
	repo := stubTorrentsRepo{}
	lookup := stubTorrentsLookup{hashes: []string{"old", "mid", "new"}}
	h := buildTorrentsHandler(t, store, lookup, repo)
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/torrents", h.Get)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/torrents", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body dto.SeriesTorrentsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Torrents, 3)
	assert.Equal(t, "new", body.Torrents[0].Hash)
	assert.Equal(t, "mid", body.Torrents[1].Hash)
	assert.Equal(t, "old", body.Torrents[2].Hash)
}

// errSeriesPort is a SeriesPort that returns an error to assert
// the 500 path through writeInternalError.
type errSeriesPort struct{}

func (errSeriesPort) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	return series.Canon{}, errors.New("db dead")
}

func (errSeriesPort) GetByTMDBID(_ context.Context, _ int) (series.Canon, error) {
	return series.Canon{}, errors.New("db dead")
}

func TestSeriesTorrents_500_OnCanonError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	store := torrentsync.NewStore()
	repo := stubTorrentsRepo{}
	lookup := stubTorrentsLookup{}
	q := torrentsync.NewQuery(store, repo, lookup)
	h := NewSeriesTorrentsHandler(q,
		stubTorrentsCachePort{entry: series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrLocal(42)}},
		errSeriesPort{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/torrents", h.Get)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/torrents", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
