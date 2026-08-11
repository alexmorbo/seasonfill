package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/moviecollection"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

func collTestLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

type fakeCollReader struct {
	parts []ports.MovieCollectionPart
	err   error
}

func (f fakeCollReader) ListPartsWithMembership(_ context.Context, _ int, _ string) ([]ports.MovieCollectionPart, error) {
	return f.parts, f.err
}

type fakeCanonReader struct {
	canon movie.CollectionCanon
	err   error
}

func (f fakeCanonReader) GetByTMDBCollectionID(_ context.Context, _ int) (movie.CollectionCanon, error) {
	return f.canon, f.err
}

type fakeMovieAdder struct{}

func (fakeMovieAdder) Add(_ context.Context, _ moviecollection.AddMovieRequest) (moviecollection.AddMovieOutcome, error) {
	return moviecollection.AddMovieOutcome{RadarrMovieID: 42}, nil
}

type fakeMonitorLookup struct {
	client moviecollection.RadarrCollectionClient
	ok     bool
}

func (f fakeMonitorLookup) Lookup(_ string) (moviecollection.RadarrCollectionClient, bool) {
	return f.client, f.ok
}

type fakeRadarrColClient struct {
	cols []ports.RadarrCollection
}

func (f fakeRadarrColClient) GetCollections(_ context.Context) ([]ports.RadarrCollection, error) {
	return f.cols, nil
}

func (f fakeRadarrColClient) PutCollection(_ context.Context, _ ports.RadarrCollection) error {
	return nil
}

type fakeMonitorStore struct{}

func (fakeMonitorStore) SetRadarrMonitored(_ context.Context, _ int, _ bool) error { return nil }

func buildCollectionsRouter(t *testing.T, h *MovieCollectionsHandler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(collTestLog()))
	r.GET("/api/v1/collections/:tmdb_collection_id", h.Get)
	r.POST("/api/v1/collections/:tmdb_collection_id/add-all-missing", h.AddAllMissing)
	r.PUT("/api/v1/collections/:tmdb_collection_id/monitor", h.Monitor)
	return r
}

func TestMovieCollections_Get_200(t *testing.T) {
	t.Parallel()
	overview := "franchise"
	reader := fakeCollReader{parts: []ports.MovieCollectionPart{
		{MovieID: 1, TMDBID: 603, Title: "The Matrix", InLibrary: true},
		{MovieID: 2, TMDBID: 604, Title: "Reloaded", InLibrary: false},
	}}
	canon := fakeCanonReader{canon: movie.CollectionCanon{TMDBCollectionID: 2344, Name: "Matrix", Overview: &overview}}
	h := NewMovieCollectionsHandler(reader, canon, nil, nil, func() string { return "main" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/collections/2344", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(2344), out["tmdb_collection_id"])
	assert.Equal(t, "main", out["instance"])
	parts, _ := out["parts"].([]any)
	require.Len(t, parts, 2)
}

func TestMovieCollections_Get_404(t *testing.T) {
	t.Parallel()
	canon := fakeCanonReader{err: errors.Join(&sharedErrors.MovieNotFoundError{}, ports.ErrNotFound)}
	h := NewMovieCollectionsHandler(fakeCollReader{}, canon, nil, nil, func() string { return "main" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/collections/99", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestMovieCollections_Get_400_NoInstance(t *testing.T) {
	t.Parallel()
	h := NewMovieCollectionsHandler(fakeCollReader{}, fakeCanonReader{}, nil, nil, func() string { return "" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/collections/2344", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMovieCollections_Get_400_BadID(t *testing.T) {
	t.Parallel()
	h := NewMovieCollectionsHandler(fakeCollReader{}, fakeCanonReader{}, nil, nil, func() string { return "main" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/collections/abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMovieCollections_AddAllMissing_200(t *testing.T) {
	t.Parallel()
	reader := fakeCollReader{parts: []ports.MovieCollectionPart{
		{MovieID: 1, TMDBID: 603, Title: "The Matrix", InLibrary: true},
		{MovieID: 2, TMDBID: 604, Title: "Reloaded", InLibrary: false},
	}}
	addAll := moviecollection.NewAddMissingUseCase(reader, fakeMovieAdder{}, collTestLog())
	h := NewMovieCollectionsHandler(reader, fakeCanonReader{}, addAll, nil, func() string { return "main" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	body := `{"instance_name":"main","quality_profile_id":6,"root_folder_path":"/movies"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/collections/2344/add-all-missing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(2), out["requested"])
	assert.Equal(t, float64(1), out["added"])
	assert.Equal(t, float64(1), out["already_present"])
}

func TestMovieCollections_AddAllMissing_400(t *testing.T) {
	t.Parallel()
	addAll := moviecollection.NewAddMissingUseCase(fakeCollReader{}, fakeMovieAdder{}, collTestLog())
	h := NewMovieCollectionsHandler(fakeCollReader{}, fakeCanonReader{}, addAll, nil, func() string { return "main" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	body := `{"instance_name":"","quality_profile_id":0,"root_folder_path":""}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/collections/2344/add-all-missing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMovieCollections_Monitor_204(t *testing.T) {
	t.Parallel()
	client := fakeRadarrColClient{cols: []ports.RadarrCollection{{ID: 5, TMDBID: 2344, Monitored: false}}}
	monitor := moviecollection.NewRadarrMonitorUseCase(fakeMonitorLookup{client: client, ok: true}, fakeMonitorStore{}, collTestLog())
	h := NewMovieCollectionsHandler(fakeCollReader{}, fakeCanonReader{}, nil, monitor, func() string { return "main" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	body := `{"instance_name":"main"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/collections/2344/monitor", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestMovieCollections_Monitor_404(t *testing.T) {
	t.Parallel()
	client := fakeRadarrColClient{cols: []ports.RadarrCollection{{ID: 5, TMDBID: 9999, Monitored: false}}}
	monitor := moviecollection.NewRadarrMonitorUseCase(fakeMonitorLookup{client: client, ok: true}, fakeMonitorStore{}, collTestLog())
	h := NewMovieCollectionsHandler(fakeCollReader{}, fakeCanonReader{}, nil, monitor, func() string { return "main" }, collTestLog())
	r := buildCollectionsRouter(t, h)

	body := `{"instance_name":"main"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/collections/2344/monitor", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "radarr_collection_not_found", out["error"])
}
