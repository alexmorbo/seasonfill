package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	grabrest "github.com/alexmorbo/seasonfill/internal/grab/rest"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// newEFRouter builds a gin engine with the F-2c-1 typed-error middleware
// so the handler's c.Error(err) dispatch reaches the JSON envelope.
func newEFRouter(h *grabrest.GrabEpisodeFilesHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(slog.Default()))
	r.GET("/instances/:name/grabs/:id/episode-files", h.List)
	return r
}

type stubSonarrEF struct {
	files []ports.EpisodeFileDetail
	err   error
}

func (s stubSonarrEF) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (s stubSonarrEF) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (s stubSonarrEF) ListSeriesCache(_ context.Context, _ shareddomain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (s stubSonarrEF) GetSeries(_ context.Context, _ shareddomain.SonarrSeriesID) (series.Series, error) {
	return series.Series{}, nil
}
func (s stubSonarrEF) ListEpisodes(_ context.Context, _ shareddomain.SonarrSeriesID, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (s stubSonarrEF) ListEpisodesBySeries(_ context.Context, _ shareddomain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (s stubSonarrEF) ListEpisodeFiles(_ context.Context, _ shareddomain.SonarrSeriesID) (map[int]int, error) {
	return map[int]int{}, nil
}
func (s stubSonarrEF) ListEpisodeFilesBySeason(_ context.Context, _ shareddomain.SonarrSeriesID, _ int) ([]ports.EpisodeFileDetail, error) {
	return s.files, s.err
}
func (s stubSonarrEF) SearchReleases(_ context.Context, _ shareddomain.SonarrSeriesID, _ int) ([]release.Release, error) {
	return nil, nil
}
func (s stubSonarrEF) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (s stubSonarrEF) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (s stubSonarrEF) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (s stubSonarrEF) GrabHistory(_ context.Context, _ shareddomain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (s stubSonarrEF) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (s stubSonarrEF) ParseRelease(_ context.Context, _ string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (s stubSonarrEF) Name() string { return "stub" }

func makeInstanceRegistry(inst map[string]scan.Instance) catalogrest.InstanceRegistry {
	return catalogrest.InstanceRegistry{
		Load: func() map[string]scan.Instance {
			return inst
		},
	}
}

func setupEFTestDB(t *testing.T) *gorm.DB {
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

func makeEFGrabRecord(t *testing.T, instance shareddomain.InstanceName, status grab.Status) grab.Record {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	return grab.Record{
		ID:           uuid.New(),
		InstanceName: instance,
		SeriesID:     122,
		SeriesTitle:  "Severance",
		SeasonNumber: 2,
		ReleaseGUID:  "g_" + uuid.New().String(),
		ReleaseTitle: "Severance.S02.PACK",
		Status:       status,
		ScanRunID:    uuid.New(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestEpisodeFiles_Imported_ReturnsItems(t *testing.T) {
	t.Parallel()
	db := setupEFTestDB(t)
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
	h := grabrest.NewGrabEpisodeFilesHandler(repo, reg, slog.Default())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:name/grabs/:id/episode-files", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/instances/main/grabs/"+rec.ID.String()+"/episode-files", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.EpisodeFileList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, 7001, resp.Items[0].ID)
	assert.Equal(t, int64(13325829734), resp.Items[0].SizeBytes)
}

func TestEpisodeFiles_NotImported_EmptyItems(t *testing.T) {
	t.Parallel()
	db := setupEFTestDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "main", grab.StatusGrabbed)
	require.NoError(t, repo.Create(context.Background(), rec))

	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{}},
	})
	h := grabrest.NewGrabEpisodeFilesHandler(repo, reg, slog.Default())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:name/grabs/:id/episode-files", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/instances/main/grabs/"+rec.ID.String()+"/episode-files", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.EpisodeFileList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
}

func TestEpisodeFiles_UnknownInstance_404(t *testing.T) {
	t.Parallel()
	db := setupEFTestDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	reg := makeInstanceRegistry(map[string]scan.Instance{})
	h := grabrest.NewGrabEpisodeFilesHandler(repo, reg, slog.Default())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:name/grabs/:id/episode-files", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/instances/unknown/grabs/"+uuid.New().String()+"/episode-files", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestEpisodeFiles_UnknownID_404(t *testing.T) {
	t.Parallel()
	db := setupEFTestDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{}},
	})
	h := grabrest.NewGrabEpisodeFilesHandler(repo, reg, slog.Default())

	// F-2c-1: this case hits the GrabRepository.GetByID NotFound path
	// which now dispatches via c.Error → typed-error middleware. The
	// router must mount the middleware for the 404 status to land.
	r := newEFRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/instances/main/grabs/"+uuid.New().String()+"/episode-files", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestEpisodeFiles_InstanceMismatch_404(t *testing.T) {
	t.Parallel()
	db := setupEFTestDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "other", grab.StatusImported)
	require.NoError(t, repo.Create(context.Background(), rec))

	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main":  {Client: stubSonarrEF{}},
		"other": {Client: stubSonarrEF{}},
	})
	h := grabrest.NewGrabEpisodeFilesHandler(repo, reg, slog.Default())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:name/grabs/:id/episode-files", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/instances/main/grabs/"+rec.ID.String()+"/episode-files", nil))
	require.Equal(t, http.StatusNotFound, w.Code, "path :name must match grab.instance_name")
}

func TestEpisodeFiles_SonarrUnavailable_502(t *testing.T) {
	t.Parallel()
	db := setupEFTestDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "main", grab.StatusImported)
	require.NoError(t, repo.Create(context.Background(), rec))

	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{err: errors.New("connection refused")}},
	})
	h := grabrest.NewGrabEpisodeFilesHandler(repo, reg, slog.Default())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:name/grabs/:id/episode-files", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/instances/main/grabs/"+rec.ID.String()+"/episode-files", nil))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestEpisodeFiles_SonarrUnauthorized_502(t *testing.T) {
	t.Parallel()
	db := setupEFTestDB(t)
	repo := grabpersistence.NewGrabRepository(db)
	rec := makeEFGrabRecord(t, "main", grab.StatusImported)
	require.NoError(t, repo.Create(context.Background(), rec))

	reg := makeInstanceRegistry(map[string]scan.Instance{
		"main": {Client: stubSonarrEF{err: domain.ErrInstanceUnauthorized}},
	})
	h := grabrest.NewGrabEpisodeFilesHandler(repo, reg, slog.Default())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:name/grabs/:id/episode-files", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/instances/main/grabs/"+rec.ID.String()+"/episode-files", nil))
	require.Equal(t, http.StatusBadGateway, w.Code)
}
