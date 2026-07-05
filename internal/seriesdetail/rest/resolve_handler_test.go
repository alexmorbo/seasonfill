package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// fakeResolveStore satisfies seriesdetail.ResolveSeriesStore for the
// handler tests. Same resolve-or-create semantics as the enrichment repo:
// GetByTMDBID → ports.ErrNotFound on miss; UpsertStub idempotent on tmdb_id.
type fakeResolveStore struct {
	byTMDB map[domain.TMDBID]domain.SeriesID
	nextID domain.SeriesID
	getErr error
}

func newFakeResolveStore() *fakeResolveStore {
	return &fakeResolveStore{byTMDB: map[domain.TMDBID]domain.SeriesID{}, nextID: 100}
}

func (f *fakeResolveStore) GetByTMDBID(_ context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	if f.getErr != nil {
		return series.Canon{}, f.getErr
	}
	if id, ok := f.byTMDB[tmdbID]; ok {
		return series.Canon{ID: id}, nil
	}
	return series.Canon{}, ports.ErrNotFound
}

func (f *fakeResolveStore) UpsertStub(_ context.Context, c series.Canon) (domain.SeriesID, error) {
	if c.TMDBID == nil {
		return 0, errors.New("fake: tmdb_id required") //nolint:err113
	}
	if id, ok := f.byTMDB[*c.TMDBID]; ok {
		return id, nil
	}
	id := f.nextID
	f.nextID++
	f.byTMDB[*c.TMDBID] = id
	return id, nil
}

func quietResolveHandlerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newResolveRouter(t *testing.T, store seriesdetail.ResolveSeriesStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	uc, err := seriesdetail.NewResolveUseCase(store, nil, quietResolveHandlerLogger())
	require.NoError(t, err)
	h := seriesdetailrest.NewResolveHandler(uc, quietResolveHandlerLogger())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietResolveHandlerLogger()))
	r.GET("/api/v1/series/resolve", h.Resolve)
	return r
}

func TestResolveHandler_200_ExistingCanon(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	store.byTMDB[1399] = 42
	r := newResolveRouter(t, store)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/resolve?tmdb_id=1399", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		SeriesID int64 `json:"series_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(42), body.SeriesID)
}

func TestResolveHandler_200_UnknownTMDB_CreatesStub(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	r := newResolveRouter(t, store)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/resolve?tmdb_id=555", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		SeriesID int64 `json:"series_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(100), body.SeriesID)
	assert.Equal(t, domain.SeriesID(100), store.byTMDB[555], "stub persisted under tmdb 555")
}

func TestResolveHandler_200_Idempotent(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	r := newResolveRouter(t, store)

	do := func() int64 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/resolve?tmdb_id=777", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			SeriesID int64 `json:"series_id"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body.SeriesID
	}
	assert.Equal(t, do(), do(), "same id across calls")
	assert.Len(t, store.byTMDB, 1, "no duplicate canon row")
}

func TestResolveHandler_400_InvalidOrMissing(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	r := newResolveRouter(t, store)

	for _, q := range []string{"", "?tmdb_id=", "?tmdb_id=0", "?tmdb_id=-5", "?tmdb_id=abc", "?tmdb_id=1.5"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/resolve"+q, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "query=%q", q)
	}
	assert.Empty(t, store.byTMDB, "invalid input never writes")
}

func TestResolveHandler_500_LookupError(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	store.getErr = errors.New("db down") //nolint:err113
	r := newResolveRouter(t, store)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/resolve?tmdb_id=900", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestResolveHandler_RouteOrder_NotCapturedByParamRoute proves the literal
// /series/resolve is dispatched to the resolve handler even when the
// /series/:id param route is registered alongside it (the collision the
// route registration guards against).
func TestResolveHandler_RouteOrder_NotCapturedByParamRoute(t *testing.T) {
	t.Parallel()
	store := newFakeResolveStore()
	store.byTMDB[1399] = 42
	gin.SetMode(gin.TestMode)
	uc, err := seriesdetail.NewResolveUseCase(store, nil, quietResolveHandlerLogger())
	require.NoError(t, err)
	h := seriesdetailrest.NewResolveHandler(uc, quietResolveHandlerLogger())

	r := gin.New()
	r.GET("/api/v1/series/resolve", h.Resolve)
	r.GET("/api/v1/series/:id", func(c *gin.Context) {
		c.String(http.StatusTeapot, "param route hit: %s", c.Param("id"))
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/resolve?tmdb_id=1399", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "resolve must win over :id param route")
	var body struct {
		SeriesID int64 `json:"series_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(42), body.SeriesID)
}
