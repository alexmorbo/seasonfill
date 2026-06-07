package handlers

import (
	"context"
	"encoding/json"
	"io"
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

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type seriesCacheFixture struct {
	db     *gorm.DB
	repo   *repositories.SeriesCacheRepository
	grabs  *repositories.GrabRepository
	router *gin.Engine
}

func newSeriesCacheFixture(t *testing.T, instances ...string) *seriesCacheFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	repo := repositories.NewSeriesCacheRepository(db)
	grabs := repositories.NewGrabRepository(db)

	instMap := map[string]scan.Instance{}
	for _, name := range instances {
		instMap[name] = scan.Instance{Config: config.SonarrInstance{Name: name, URL: "http://x", Mode: "auto"}}
	}
	reg := InstanceRegistry{Load: func() map[string]scan.Instance { return instMap }}
	checker := &healthcheck.Checker{}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewInstancesHandler(checker, reg, lg).WithSeriesCache(repo)

	r := gin.New()
	api := r.Group("/api/v1")
	api.GET("/instances/:name/series-cache", h.ListSeriesCache)

	return &seriesCacheFixture{db: db, repo: repo, grabs: grabs, router: r}
}

func (f *seriesCacheFixture) seed(t *testing.T, instance string, id int, title string, missing int, updatedAt time.Time) {
	t.Helper()
	year := 2024
	poster := "/MediaCover/" + title + "/poster.jpg"
	network := "Apple TV+"
	entry := series.CacheEntry{
		InstanceName: instance, SonarrSeriesID: id,
		Title: title, TitleSlug: title,
		Year: &year, Network: &network, PosterPath: &poster,
		Monitored:    true,
		MissingCount: missing,
	}
	require.NoError(t, f.repo.Upsert(context.Background(), entry))
	require.NoError(t, f.db.Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instance, id).
		Update("updated_at", updatedAt).Error)
}

func (f *seriesCacheFixture) seedImportedGrab(t *testing.T, instance string, seriesID, season int, createdAt time.Time) {
	t.Helper()
	require.NoError(t, f.grabs.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: instance, SeriesID: seriesID,
		SeasonNumber: season, ScanRunID: uuid.New(),
		Status: grab.StatusImported, CreatedAt: createdAt, UpdatedAt: createdAt,
	}))
}

func (f *seriesCacheFixture) do(t *testing.T, path string) (*httptest.ResponseRecorder, dto.SeriesCacheList) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var body dto.SeriesCacheList
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return rec, body
}

func TestInstancesHandler_ListSeriesCache_StateAll_HappyPath(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	for i := 1; i <= 3; i++ {
		f.seed(t, "homelab", i, "Series"+string(rune('0'+i)), 0, now.Add(time.Duration(i)*time.Minute))
	}

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, body.Total)
	assert.False(t, body.HasMore)
	require.Len(t, body.Items, 3)
	assert.Equal(t, 3, body.Items[0].SonarrSeriesID, "updated_desc default — newest first")
}

func TestInstancesHandler_ListSeriesCache_StateImported(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Alpha", 0, now)
	f.seed(t, "homelab", 2, "Beta", 0, now)
	f.seedImportedGrab(t, "homelab", 1, 5, now.Add(-2*time.Hour))

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?state=imported")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 1)
	assert.Equal(t, 1, body.Items[0].SonarrSeriesID)
	assert.Equal(t, "S05", body.Items[0].LastImportedEpisode)
	require.NotNil(t, body.Items[0].LastGrabAt)
}

func TestInstancesHandler_ListSeriesCache_StateMissing(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Alpha", 0, now)
	f.seed(t, "homelab", 2, "Beta", 7, now)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?state=missing")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 1)
	assert.Equal(t, 2, body.Items[0].SonarrSeriesID)
	assert.Equal(t, 7, body.Items[0].MissingCount)
}

func TestInstancesHandler_ListSeriesCache_StatusAliasAccepted(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	f.seed(t, "homelab", 1, "Alpha", 7, time.Now().UTC())
	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?status=missing")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 1)
}

func TestInstancesHandler_ListSeriesCache_TitleAsc(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Zulu", 0, now)
	f.seed(t, "homelab", 2, "Alpha", 0, now)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?sort=title_asc")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 2)
	assert.Equal(t, "Alpha", body.Items[0].Title)
	assert.Equal(t, "Zulu", body.Items[1].Title)
}

func TestInstancesHandler_ListSeriesCache_KeysetPaginates(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	for i := 1; i <= 30; i++ {
		f.seed(t, "homelab", i, "S", 0, now.Add(time.Duration(i)*time.Minute))
	}
	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?limit=12")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 30, body.Total)
	assert.True(t, body.HasMore)
	require.Len(t, body.Items, 12)
	require.NotEmpty(t, body.NextCursor)

	rec, body2 := f.do(t, "/api/v1/instances/homelab/series-cache?limit=12&cursor="+body.NextCursor)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body2.Items, 12)
	require.NotEmpty(t, body2.NextCursor)

	rec, body3 := f.do(t, "/api/v1/instances/homelab/series-cache?limit=12&cursor="+body2.NextCursor)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body3.Items, 6)
	assert.False(t, body3.HasMore)
	assert.Empty(t, body3.NextCursor)

	seen := map[int]bool{}
	for _, p := range [][]dto.SeriesCacheItem{body.Items, body2.Items, body3.Items} {
		for _, it := range p {
			assert.False(t, seen[it.SonarrSeriesID], "no duplicates across pages")
			seen[it.SonarrSeriesID] = true
		}
	}
	assert.Len(t, seen, 30)
}

func TestInstancesHandler_ListSeriesCache_BadState(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache?state=bogus")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInstancesHandler_ListSeriesCache_BadSort(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache?sort=ascending")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInstancesHandler_ListSeriesCache_BadLimit(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache?limit=99999")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInstancesHandler_ListSeriesCache_BadCursor(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache?cursor=not-base64")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInstancesHandler_ListSeriesCache_UnknownInstance(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	rec, _ := f.do(t, "/api/v1/instances/ghost/series-cache")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
