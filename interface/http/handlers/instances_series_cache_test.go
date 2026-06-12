package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
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

	repo := repositories.NewSeriesCacheRepository(db, repositories.NewSeriesRepository(db))
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
	api.GET("/instances/:name/series-cache/networks", h.ListSeriesCacheNetworks)

	return &seriesCacheFixture{db: db, repo: repo, grabs: grabs, router: r}
}

// seedWith — Story 121a: lets a test seed a row and then mutate
// additional fields (Monitored, Network) before Upsert.
func (f *seriesCacheFixture) seedWith(
	t *testing.T,
	instance string,
	id int,
	title string,
	missing int,
	ts time.Time,
	mutate func(*series.CacheEntry),
) {
	t.Helper()
	year := 2024
	poster := "/MediaCover/" + title + "/poster.jpg"
	e := series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: id,
		Title:          title,
		TitleSlug:      strings.ToLower(strings.ReplaceAll(title, " ", "-")),
		Year:           &year,
		PosterPath:     &poster,
		Monitored:      true,
		MissingCount:   missing,
		UpdatedAt:      ts,
	}
	if mutate != nil {
		mutate(&e)
	}
	require.NoError(t, f.repo.Upsert(context.Background(), e))
	require.NoError(t, f.db.Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instance, id).
		Update("updated_at", ts).Error)
}

func (f *seriesCacheFixture) seed(t *testing.T, instance string, id int, title string, missing int, updatedAt time.Time) {
	t.Helper()
	year := 2024
	poster := "/MediaCover/" + title + "/poster.jpg"
	entry := series.CacheEntry{
		InstanceName: instance, SonarrSeriesID: id,
		Title: title, TitleSlug: title,
		Year: &year, PosterPath: &poster,
		Monitored:    true,
		MissingCount: missing,
	}
	require.NoError(t, f.repo.Upsert(context.Background(), entry))
	require.NoError(t, f.db.Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instance, id).
		Update("updated_at", updatedAt).Error)
}

func (f *seriesCacheFixture) seedAired(t *testing.T, instance string, id int, title string, lastAired *time.Time, updatedAt time.Time) {
	t.Helper()
	year := 2024
	poster := "/MediaCover/" + title + "/poster.jpg"
	entry := series.CacheEntry{
		InstanceName: instance, SonarrSeriesID: id,
		Title: title, TitleSlug: title,
		Year: &year, PosterPath: &poster,
		Monitored:   true,
		LastAiredAt: lastAired,
	}
	require.NoError(t, f.repo.Upsert(context.Background(), entry))
	require.NoError(t, f.db.Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instance, id).
		Update("updated_at", updatedAt).Error)
}

// seedNetworkForSeries writes a (networks, series_networks) row for the
// given series_cache (instance, sonarr_id). E-1: post-cutover the
// network filter reads from the series_networks join; tests must seed
// the join directly because CacheEntry no longer carries Network.
func (f *seriesCacheFixture) seedNetworkForSeries(t *testing.T, instance string, sonarrID int, name string) {
	t.Helper()
	if name == "" {
		return
	}
	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "series_cache row must have a resolved series_id")
	netRepo := repositories.NewNetworksRepository(f.db)
	id, err := netRepo.ResolveByName(context.Background(), name)
	if err != nil {
		id, err = netRepo.Upsert(context.Background(), taxonomy.Network{Name: name})
		require.NoError(t, err)
	}
	require.NoError(t, f.db.Clauses(clauseOnConflictDoNothing()).Create(&database.SeriesNetworkModel{
		SeriesID:  *sc.SeriesID,
		NetworkID: id,
	}).Error)
}

func clauseOnConflictDoNothing() clause.OnConflict {
	return clause.OnConflict{DoNothing: true}
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

func TestInstancesHandler_ListSeriesCache_AirDateDesc(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	t1 := now.Add(-30 * 24 * time.Hour)
	t2 := now.Add(-2 * 24 * time.Hour)

	f.seedAired(t, "homelab", 1, "OldAirer", &t1, now)
	f.seedAired(t, "homelab", 2, "NewAirer", &t2, now)
	f.seedAired(t, "homelab", 3, "Upcoming", nil, now)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?sort=air_date_desc")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 3)
	assert.Equal(t, 2, body.Items[0].SonarrSeriesID, "newest aired first")
	require.NotNil(t, body.Items[0].LastAiredAt)
	assert.True(t, body.Items[0].LastAiredAt.Equal(t2))
	assert.Equal(t, 1, body.Items[1].SonarrSeriesID, "older aired second")
	assert.Equal(t, 3, body.Items[2].SonarrSeriesID, "nil aired last")
	assert.Nil(t, body.Items[2].LastAiredAt)
}

func TestInstancesHandler_ListSeriesCache_QFiltersByTitle(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Rick and Morty", 0, now)
	f.seed(t, "homelab", 2, "Severance", 0, now)
	f.seed(t, "homelab", 3, "For All Mankind", 0, now)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?q=rick")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, body.Total, "total reflects post-q count, not the pre-filter set")
	require.Len(t, body.Items, 1)
	assert.Equal(t, 1, body.Items[0].SonarrSeriesID)
}

func TestInstancesHandler_ListSeriesCache_QCombinedWithState(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Rick and Morty", 0, now)   // not missing
	f.seed(t, "homelab", 2, "Rick and Friends", 5, now) // missing
	f.seed(t, "homelab", 3, "Severance", 7, now)        // missing

	// q="rick" + state=missing must intersect to a single row.
	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?q=rick&state=missing")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, body.Total)
	require.Len(t, body.Items, 1)
	assert.Equal(t, 2, body.Items[0].SonarrSeriesID)
}

func TestInstancesHandler_ListSeriesCache_QEmptyMeansNoFilter(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Alpha", 0, now)
	f.seed(t, "homelab", 2, "Beta", 0, now)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?q=")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 2, body.Total)
}

func TestInstancesHandler_ListSeriesCache_QOverLong400(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	long := strings.Repeat("x", 201)
	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache?q="+long)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestInstancesHandler_ListSeriesCache_MonitoredFilter — Story 121a §A
func TestInstancesHandler_ListSeriesCache_MonitoredFilter(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seedWith(t, "homelab", 1, "Rick and Morty", 0, now, func(e *series.CacheEntry) {
		e.Monitored = true
	})
	f.seedWith(t, "homelab", 2, "Severance", 0, now, func(e *series.CacheEntry) {
		e.Monitored = false
	})

	cases := []struct {
		url     string
		wantIDs []int
	}{
		{"/api/v1/instances/homelab/series-cache?monitored=1", []int{1}},
		{"/api/v1/instances/homelab/series-cache?monitored=true", []int{1}},
		{"/api/v1/instances/homelab/series-cache?monitored=0", []int{2}},
		{"/api/v1/instances/homelab/series-cache?monitored=false", []int{2}},
		{"/api/v1/instances/homelab/series-cache", []int{1, 2}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.url, func(t *testing.T) {
			rec, body := f.do(t, tc.url)
			require.Equal(t, http.StatusOK, rec.Code)
			gotIDs := make([]int, 0, len(body.Items))
			for _, it := range body.Items {
				gotIDs = append(gotIDs, it.SonarrSeriesID)
			}
			assert.ElementsMatch(t, tc.wantIDs, gotIDs)
		})
	}
}

func TestInstancesHandler_ListSeriesCache_MonitoredInvalid400(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache?monitored=maybe")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestInstancesHandler_ListSeriesCache_NetworksFilter — Story 121a §A,
// updated for E-1 (Story 210): network membership lives in
// series_networks, not on series.network; tests seed the join.
func TestInstancesHandler_ListSeriesCache_NetworksFilter(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seedWith(t, "homelab", 1, "A", 0, now, nil)
	f.seedWith(t, "homelab", 2, "B", 0, now, nil)
	f.seedWith(t, "homelab", 3, "C", 0, now, nil)
	f.seedNetworkForSeries(t, "homelab", 1, "HBO")
	f.seedNetworkForSeries(t, "homelab", 2, "Apple TV+")
	f.seedNetworkForSeries(t, "homelab", 3, "Netflix")

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?networks=HBO|Netflix")
	require.Equal(t, http.StatusOK, rec.Code)
	gotIDs := make([]int, 0, len(body.Items))
	for _, it := range body.Items {
		gotIDs = append(gotIDs, it.SonarrSeriesID)
	}
	assert.ElementsMatch(t, []int{1, 3}, gotIDs)
}

func TestInstancesHandler_ListSeriesCache_NetworksTooMany400(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	long := strings.Repeat("X|", 33)
	long = long[:len(long)-1] // trim trailing pipe
	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache?networks="+long)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestInstancesHandler_ListSeriesCacheNetworks_HappyPath — Story 121a §A
func TestInstancesHandler_ListSeriesCacheNetworks_HappyPath(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seedWith(t, "homelab", 1, "A", 0, now, nil)
	f.seedWith(t, "homelab", 2, "B", 0, now, nil)
	f.seedWith(t, "homelab", 3, "C", 0, now, nil)
	f.seedNetworkForSeries(t, "homelab", 1, "HBO")
	f.seedNetworkForSeries(t, "homelab", 2, "Apple TV+")
	f.seedNetworkForSeries(t, "homelab", 3, "HBO")

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/homelab/series-cache/networks", nil)
	f.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body dto.SeriesCacheNetworksList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []string{"Apple TV+", "HBO"}, body.Networks)
}

func TestInstancesHandler_ListSeriesCacheNetworks_UnknownInstance404(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/nope/series-cache/networks", nil)
	f.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
