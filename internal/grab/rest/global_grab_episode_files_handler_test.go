package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	grabrest "github.com/alexmorbo/seasonfill/internal/grab/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Story 492 / N-1b — global grab episode-files wrapper tests. The
// global handler resolves the instance from the persisted grab_records
// row rather than from a path param, so the per-instance handler's
// path-mismatch defence-in-depth case is moot here. Other paths
// (200 happy / empty-when-not-imported / 502 upstream / 404 unknown id /
// 400 bad UUID / 404 unknown instance) mirror the per-instance coverage.

func setupGlobalEFDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return db
}

func newGlobalEFRouter(h *grabrest.GlobalGrabEpisodeFilesHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(slog.Default()))
	r.GET("/grabs/:id/episode-files", h.List)
	return r
}

func TestGlobalGrabEpisodeFiles_Imported_ReturnsItems(t *testing.T) {
	t.Parallel()
	db := setupGlobalEFDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "main", grab.StatusImported)
	require.NoError(t, repo.Create(context.Background(), rec))

	files := []ports.EpisodeFileDetail{
		{ID: 7001, RelativePath: "Season 02/S02E01.mkv", SeasonNumber: 2,
			EpisodeNumbers: []int{1}, SizeBytes: 13325829734, Quality: "WEBDL-2160p"},
	}
	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{files: files}},
	})
	h := grabrest.NewGlobalGrabEpisodeFilesHandler(repo, reg, slog.Default())

	r := newGlobalEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/grabs/"+rec.ID.String()+"/episode-files", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.EpisodeFileList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, 7001, resp.Items[0].ID)
	assert.Equal(t, int64(13325829734), resp.Items[0].SizeBytes)
}

func TestGlobalGrabEpisodeFiles_NotImported_EmptyItems(t *testing.T) {
	t.Parallel()
	db := setupGlobalEFDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "main", grab.StatusGrabbed)
	require.NoError(t, repo.Create(context.Background(), rec))

	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{}},
	})
	h := grabrest.NewGlobalGrabEpisodeFilesHandler(repo, reg, slog.Default())

	r := newGlobalEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/grabs/"+rec.ID.String()+"/episode-files", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.EpisodeFileList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
}

func TestGlobalGrabEpisodeFiles_BadUUID_400(t *testing.T) {
	t.Parallel()
	db := setupGlobalEFDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	reg := makeInstanceRegistry(map[string]scan.Instance{})
	h := grabrest.NewGlobalGrabEpisodeFilesHandler(repo, reg, slog.Default())

	r := newGlobalEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/grabs/not-a-uuid/episode-files", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGlobalGrabEpisodeFiles_UnknownID_404(t *testing.T) {
	t.Parallel()
	db := setupGlobalEFDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{}},
	})
	h := grabrest.NewGlobalGrabEpisodeFilesHandler(repo, reg, slog.Default())

	r := newGlobalEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/grabs/"+uuid.New().String()+"/episode-files", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGlobalGrabEpisodeFiles_InstanceNoLongerConfigured_404(t *testing.T) {
	t.Parallel()
	db := setupGlobalEFDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "removed", grab.StatusImported)
	require.NoError(t, repo.Create(context.Background(), rec))

	// Registry has a different instance — the recorded "removed" was
	// deconfigured between grab time and lookup time.
	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{}},
	})
	h := grabrest.NewGlobalGrabEpisodeFilesHandler(repo, reg, slog.Default())

	r := newGlobalEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/grabs/"+rec.ID.String()+"/episode-files", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "removed")
}

func TestGlobalGrabEpisodeFiles_SonarrUnavailable_502(t *testing.T) {
	t.Parallel()
	db := setupGlobalEFDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "main", grab.StatusImported)
	require.NoError(t, repo.Create(context.Background(), rec))

	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{err: errors.New("connection refused")}}, //nolint:err113
	})
	h := grabrest.NewGlobalGrabEpisodeFilesHandler(repo, reg, slog.Default())

	r := newGlobalEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/grabs/"+rec.ID.String()+"/episode-files", nil))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestGlobalGrabEpisodeFiles_SonarrUnauthorized_502(t *testing.T) {
	t.Parallel()
	db := setupGlobalEFDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "main", grab.StatusImported)
	require.NoError(t, repo.Create(context.Background(), rec))

	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{err: sharedErrors.ErrInstanceUnauthorized}},
	})
	h := grabrest.NewGlobalGrabEpisodeFilesHandler(repo, reg, slog.Default())

	r := newGlobalEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/grabs/"+rec.ID.String()+"/episode-files", nil))
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "unauthorized")
}
