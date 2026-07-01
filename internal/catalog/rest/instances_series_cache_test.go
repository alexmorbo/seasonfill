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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type seriesCacheFixture struct {
	db     *gorm.DB
	repo   *catalogpersistence.SeriesCacheRepository
	grabs  *grabpersistence.GrabRepository
	router *gin.Engine
}

func newSeriesCacheFixture(t *testing.T, instances ...string) *seriesCacheFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	// Story 352: the EnsurePending kick runs in a background
	// goroutine which acquires a fresh sqlite connection — :memory:
	// connections get isolated databases, so without single-conn
	// pinning the goroutine's writes land on an unmigrated DB. Other
	// fixtures that ran only on the request-serving connection don't
	// need this; we add it here because we touch media_assets from
	// the background goroutine.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	repo := catalogpersistence.NewSeriesCacheRepository(db, enrichpersistence.NewSeriesRepository(db))
	grabs := grabpersistence.NewGrabRepository(db)

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

// withMediaPending widens the fixture to also wire a
// MediaAssetsRepository as the InstancesHandler's CatalogMediaPendingWriter.
// Used by the story 352 tests that assert the EnsurePending kick fired
// after the response committed.
//
// Returns the underlying *MediaAssetsRepository so the test can query
// media_assets rows for assertions.
func (f *seriesCacheFixture) withMediaPending(t *testing.T) *enrichpersistence.MediaAssetsRepository {
	t.Helper()
	mediaRepo := enrichpersistence.NewMediaAssetsRepository(f.db)

	// Rebuild the router with a fresh handler chain that includes
	// WithMediaPending. The fixture's existing router has the handler
	// registered without the writer, so we replace it.
	instMap := map[string]scan.Instance{}
	// Re-read instance set from the existing route registration is
	// impossible — restore from a sentinel. The fixture builder seeds
	// "homelab" as the canonical name; tests that need additional
	// instances should not use this helper.
	instMap["homelab"] = scan.Instance{Config: config.SonarrInstance{Name: "homelab", URL: "http://x", Mode: "auto"}}
	reg := InstanceRegistry{Load: func() map[string]scan.Instance { return instMap }}
	checker := &healthcheck.Checker{}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewInstancesHandler(checker, reg, lg).
		WithSeriesCache(f.repo).
		WithMediaPending(mediaRepo)

	r := gin.New()
	api := r.Group("/api/v1")
	api.GET("/instances/:name/series-cache", h.ListSeriesCache)
	api.GET("/instances/:name/series-cache/networks", h.ListSeriesCacheNetworks)
	api.GET("/instances/:name/missing", h.Missing)
	f.router = r
	return mediaRepo
}

// seedWith — Story 121a: lets a test seed a row and then mutate
// additional fields (Monitored, Network) before Upsert.
func (f *seriesCacheFixture) seedWith(
	t *testing.T,
	instance shareddomain.InstanceName,
	id shareddomain.SonarrSeriesID,
	title string,
	missing int,
	ts time.Time,
	mutate func(*series.CacheEntry),
) {
	t.Helper()
	year := 2024
	e := series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: id,
		Title:          title,
		TitleSlug:      strings.ToLower(strings.ReplaceAll(title, " ", "-")),
		Year:           &year,
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

func (f *seriesCacheFixture) seed(t *testing.T, instance shareddomain.InstanceName, id shareddomain.SonarrSeriesID, title string, missing int, updatedAt time.Time) {
	t.Helper()
	year := 2024
	entry := series.CacheEntry{
		InstanceName: instance, SonarrSeriesID: id,
		Title: title, TitleSlug: title,
		Year:         &year,
		Monitored:    true,
		MissingCount: missing,
	}
	require.NoError(t, f.repo.Upsert(context.Background(), entry))
	require.NoError(t, f.db.Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instance, id).
		Update("updated_at", updatedAt).Error)
}

func (f *seriesCacheFixture) seedAired(t *testing.T, instance shareddomain.InstanceName, id shareddomain.SonarrSeriesID, title string, lastAired *time.Time, updatedAt time.Time) {
	t.Helper()
	year := 2024
	entry := series.CacheEntry{
		InstanceName: instance, SonarrSeriesID: id,
		Title: title, TitleSlug: title,
		Year:        &year,
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
func (f *seriesCacheFixture) seedNetworkForSeries(t *testing.T, instance shareddomain.InstanceName, sonarrID int, name string) {
	t.Helper()
	if name == "" {
		return
	}
	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "series_cache row must have a resolved series_id")
	netRepo := enrichpersistence.NewNetworksRepository(f.db)
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

func (f *seriesCacheFixture) seedImportedGrab(t *testing.T, instance shareddomain.InstanceName, seriesID shareddomain.SonarrSeriesID, season int, createdAt time.Time) {
	t.Helper()
	require.NoError(t, f.grabs.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: instance, SeriesID: seriesID,
		SeasonNumber: season, ScanRunID: uuid.New(),
		Status: grab.StatusImported, CreatedAt: createdAt, UpdatedAt: createdAt,
	}))
}

// withLocalizer rebuilds the router with a handler that also wires the
// given series-title localizer. Hardcodes "homelab" like withMediaPending.
// Story E-1-B7.
func (f *seriesCacheFixture) withLocalizer(t *testing.T, loc SeriesTextLocalizer) {
	t.Helper()
	instMap := map[string]scan.Instance{
		"homelab": {Config: config.SonarrInstance{Name: "homelab", URL: "http://x", Mode: "auto"}},
	}
	reg := InstanceRegistry{Load: func() map[string]scan.Instance { return instMap }}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewInstancesHandler(&healthcheck.Checker{}, reg, lg).
		WithSeriesCache(f.repo).
		WithLocalizer(loc)
	r := gin.New()
	api := r.Group("/api/v1")
	api.GET("/instances/:name/series-cache", h.ListSeriesCache)
	f.router = r
}

// canonSeriesID reads the resolved canon series.id for a seeded
// (instance, sonarr_id) series_cache row. Story E-1-B7.
func (f *seriesCacheFixture) canonSeriesID(t *testing.T, instance shareddomain.InstanceName, sonarrID shareddomain.SonarrSeriesID) shareddomain.SeriesID {
	t.Helper()
	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "seeded row must have a resolved canon series_id")
	return *sc.SeriesID
}

// fakeCatalogLocalizer counts calls (to assert no N+1) and returns a
// seeded title map. Satisfies rest.SeriesTextLocalizer. Story E-1-B7.
type fakeCatalogLocalizer struct {
	calls    int
	titles   map[shareddomain.SeriesID]string
	lastLang string
	err      error
}

func (fl *fakeCatalogLocalizer) ListByIDsWithFallback(
	_ context.Context, ids []shareddomain.SeriesID, lang string,
) (map[shareddomain.SeriesID]series.SeriesText, error) {
	fl.calls++
	fl.lastLang = lang
	if fl.err != nil {
		return nil, fl.err
	}
	out := make(map[shareddomain.SeriesID]series.SeriesText, len(ids))
	for _, id := range ids {
		if title, ok := fl.titles[id]; ok {
			t := title
			out[id] = series.SeriesText{SeriesID: id, Language: lang, Title: &t}
		}
	}
	return out, nil
}

// newLocalizingHandler builds a handler for direct helper-level tests
// (no HTTP/reg dependency). loc=nil leaves the localizer unwired.
func newLocalizingHandler(loc SeriesTextLocalizer) *InstancesHandler {
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewInstancesHandler(&healthcheck.Checker{}, InstanceRegistry{}, lg)
	if loc != nil {
		h = h.WithLocalizer(loc)
	}
	return h
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
		f.seed(t, "homelab", shareddomain.SonarrSeriesID(i), "Series"+string(rune('0'+i)), 0, now.Add(time.Duration(i)*time.Minute))
	}

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, body.Total)
	assert.False(t, body.HasMore)
	require.Len(t, body.Items, 3)
	assert.Equal(t, shareddomain.SonarrSeriesID(3), body.Items[0].SonarrSeriesID, "updated_desc default — newest first")
}

// The /series-cache list serializes `poster_hash` for every row whose
// canon poster_asset is set, deriving the hash deterministically from
// the synthetic w342 CDN URL. The downloader having reached
// status='stored' on media_assets is NOT a precondition — the FE
// requests /media/<hash> immediately, and the media handler's on-
// demand fetch fills the bytes. Rows without a canon path omit the
// field (FE falls back to monogram).
func TestInstancesHandler_ListSeriesCache_PosterHash(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "WithPath", 0, now)
	f.seed(t, "homelab", 2, "Pathless", 0, now.Add(time.Minute))

	// Stamp the canon poster_asset on series 1. Deliberately do NOT
	// write a media_assets row — the new behavior is "canon path → hash
	// projected regardless of media_assets state".
	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", "homelab", 1,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID)
	require.NoError(t, f.db.Model(&database.SeriesModel{}).
		Where("id = ?", *sc.SeriesID).
		Update("poster_asset", "/warmed.jpg").Error)

	expectedHash := appmedia.HashFromURL(
		appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, "/warmed.jpg"),
	)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 2)

	byID := map[shareddomain.SonarrSeriesID]dto.SeriesCacheItem{}
	for _, it := range body.Items {
		byID[it.SonarrSeriesID] = it
	}
	// B-42a: series_id must be exposed for FE link navigation.
	for _, it := range body.Items {
		require.NotNil(t, it.SeriesID, "series_cache DTO must expose canonical series_id (B-42a)")
	}
	require.NotNil(t, byID[1].PosterHash, "row with canon path → poster_hash derived")
	assert.Equal(t, expectedHash, *byID[1].PosterHash)
	assert.Nil(t, byID[2].PosterHash, "row without canon path → poster_hash absent")

	// Verify the wire JSON actually carries poster_hash (omitempty
	// must not strip it when present).
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	raw := rec.Body.String()
	assert.Contains(t, raw, `"poster_hash":"`+expectedHash+`"`,
		"poster_hash field present in wire JSON for row with canon path")
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
	assert.Equal(t, shareddomain.SonarrSeriesID(1), body.Items[0].SonarrSeriesID)
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
	assert.Equal(t, shareddomain.SonarrSeriesID(2), body.Items[0].SonarrSeriesID)
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
		f.seed(t, "homelab", shareddomain.SonarrSeriesID(i), "S", 0, now.Add(time.Duration(i)*time.Minute))
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

	seen := map[shareddomain.SonarrSeriesID]bool{}
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
	assert.Equal(t, shareddomain.SonarrSeriesID(2), body.Items[0].SonarrSeriesID, "newest aired first")
	require.NotNil(t, body.Items[0].LastAiredAt)
	assert.True(t, body.Items[0].LastAiredAt.Equal(t2))
	assert.Equal(t, shareddomain.SonarrSeriesID(1), body.Items[1].SonarrSeriesID, "older aired second")
	assert.Equal(t, shareddomain.SonarrSeriesID(3), body.Items[2].SonarrSeriesID, "nil aired last")
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
	assert.Equal(t, shareddomain.SonarrSeriesID(1), body.Items[0].SonarrSeriesID)
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
	assert.Equal(t, shareddomain.SonarrSeriesID(2), body.Items[0].SonarrSeriesID)
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
		t.Run(tc.url, func(t *testing.T) {
			rec, body := f.do(t, tc.url)
			require.Equal(t, http.StatusOK, rec.Code)
			gotIDs := make([]int, 0, len(body.Items))
			for _, it := range body.Items {
				gotIDs = append(gotIDs, int(it.SonarrSeriesID))
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
		gotIDs = append(gotIDs, int(it.SonarrSeriesID))
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

// Story 352: the /series-cache endpoint must land a pending
// media_assets row for every series whose canon poster_asset is set,
// keyed on the same eager hash projected into the wire DTO. The
// EnsurePending call runs in a background goroutine after the response
// commits; the test polls media_assets until the row appears (2-second
// deadline). On-demand fetch (run by GET /api/v1/media/<hash>) then
// reads the row to recover the source_url + kind without an extra
// catalog lookup.
func TestInstancesHandler_ListSeriesCache_EnsuresPendingMediaAssets(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	mediaRepo := f.withMediaPending(t)
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "WithPath", 0, now)

	// Stamp the canon poster_asset on series 1.
	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", "homelab", 1,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID)
	require.NoError(t, f.db.Model(&database.SeriesModel{}).
		Where("id = ?", *sc.SeriesID).
		Update("poster_asset", "/poster.jpg").Error)

	expectedURL := appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, "/poster.jpg")
	expectedHash := appmedia.HashFromURL(expectedURL)

	rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache")
	require.Equal(t, http.StatusOK, rec.Code)

	// Poll media_assets for the pending row. 2-second deadline + 10 ms
	// step balances test wall-time against the goroutine's wake-up
	// latency under -race (~ms on a hot CPU).
	deadline := time.Now().Add(2 * time.Second)
	var asset media.Asset
	for time.Now().Before(deadline) {
		a, err := mediaRepo.Get(context.Background(), expectedHash)
		if err == nil {
			asset = a
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, expectedHash, asset.Hash, "media_assets row must exist for the eager hash")
	assert.Equal(t, expectedURL, asset.UpstreamURL)
	assert.Equal(t, "poster_w342", asset.Kind)
	assert.Equal(t, media.StatusPending, asset.Status)
}

// Two concurrent /series-cache requests for the same series must
// produce exactly ONE media_assets row (ON CONFLICT (hash) DO NOTHING).
func TestInstancesHandler_ListSeriesCache_EnsurePendingIsRaceSafe(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	mediaRepo := f.withMediaPending(t)
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "WithPath", 0, now)

	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", "homelab", 1,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID)
	require.NoError(t, f.db.Model(&database.SeriesModel{}).
		Where("id = ?", *sc.SeriesID).
		Update("poster_asset", "/race.jpg").Error)

	expectedHash := appmedia.HashFromURL(
		appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, "/race.jpg"),
	)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			rec, _ := f.do(t, "/api/v1/instances/homelab/series-cache")
			require.Equal(t, http.StatusOK, rec.Code)
		})
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := mediaRepo.Get(context.Background(), expectedHash); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	var count int64
	require.NoError(t, f.db.Table("media_assets").
		Where("hash = ?", expectedHash).
		Count(&count).Error)
	assert.Equal(t, int64(1), count, "ON CONFLICT (hash) DO NOTHING — exactly one row")
}

// Story 352: the /missing endpoint enriches items via
// enrichMissingFromCache which also projects eager poster_hash values.
// The same EnsurePending kick must fire so the media handler's
// on-demand path can recover from /api/v1/media/<hash>.
//
// Note: the /missing endpoint's full Missing handler requires a live
// Sonarr client (for episode counts). This test invokes
// enrichMissingFromCache directly with synthetic items to isolate the
// EnsurePending behaviour from the upstream wiring.
func TestInstancesHandler_EnrichMissingFromCache_EnsuresPendingMediaAssets(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	mediaRepo := f.withMediaPending(t)
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "WithPath", 0, now)

	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", "homelab", 1,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID)
	require.NoError(t, f.db.Model(&database.SeriesModel{}).
		Where("id = ?", *sc.SeriesID).
		Update("poster_asset", "/missing.jpg").Error)

	expectedHash := appmedia.HashFromURL(
		appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, "/missing.jpg"),
	)

	// Rebuild a handler that exposes enrichMissingFromCache directly.
	instMap := map[string]scan.Instance{
		"homelab": {Config: config.SonarrInstance{Name: "homelab", URL: "http://x", Mode: "auto"}},
	}
	reg := InstanceRegistry{Load: func() map[string]scan.Instance { return instMap }}
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewInstancesHandler(&healthcheck.Checker{}, reg, lg).
		WithSeriesCache(f.repo).
		WithMediaPending(mediaRepo)

	items := []dto.MissingSeries{{SeriesID: 1}}
	h.enrichMissingFromCache(context.Background(), "homelab", items)
	require.NotNil(t, items[0].PosterHash)
	assert.Equal(t, expectedHash, *items[0].PosterHash)

	deadline := time.Now().Add(2 * time.Second)
	var asset media.Asset
	for time.Now().Before(deadline) {
		a, err := mediaRepo.Get(context.Background(), expectedHash)
		if err == nil {
			asset = a
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, expectedHash, asset.Hash)
	assert.Equal(t, media.StatusPending, asset.Status)
}

// Story E-1-B7 — ?lang=ru-RU overrides item titles in a single batch
// call (no N+1), raw BCP-47 pass-through.
func TestInstancesHandler_ListSeriesCache_Localize_RuRU(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Rick and Morty", 0, now)
	f.seed(t, "homelab", 2, "Friends", 0, now.Add(time.Minute))
	canon1 := f.canonSeriesID(t, "homelab", 1)
	canon2 := f.canonSeriesID(t, "homelab", 2)
	loc := &fakeCatalogLocalizer{titles: map[shareddomain.SeriesID]string{
		canon1: "Рик и Морти",
		canon2: "Друзья",
	}}
	f.withLocalizer(t, loc)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?lang=ru-RU")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 2)
	byID := map[shareddomain.SonarrSeriesID]string{}
	for _, it := range body.Items {
		byID[it.SonarrSeriesID] = it.Title
	}
	assert.Equal(t, "Рик и Морти", byID[1])
	assert.Equal(t, "Друзья", byID[2])
	assert.Equal(t, 1, loc.calls, "single batch call, no N+1")
	assert.Equal(t, "ru-RU", loc.lastLang, "raw BCP-47 pass-through, not normalized")
}

// Empty ?lang= → canon titles, zero DB calls (non-breaking).
func TestInstancesHandler_ListSeriesCache_Localize_EmptyLangZeroCalls(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Rick and Morty", 0, now)
	canon1 := f.canonSeriesID(t, "homelab", 1)
	loc := &fakeCatalogLocalizer{titles: map[shareddomain.SeriesID]string{canon1: "Рик и Морти"}}
	f.withLocalizer(t, loc)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "Rick and Morty", body.Items[0].Title, "canon unchanged without ?lang=")
	assert.Equal(t, 0, loc.calls, "zero DB work when lang absent")
}

// Map miss → canon title retained, still one batch call.
func TestInstancesHandler_ListSeriesCache_Localize_MapMissKeepsCanon(t *testing.T) {
	t.Parallel()
	f := newSeriesCacheFixture(t, "homelab")
	now := time.Now().UTC()
	f.seed(t, "homelab", 1, "Rick and Morty", 0, now)
	loc := &fakeCatalogLocalizer{titles: map[shareddomain.SeriesID]string{}} // miss
	f.withLocalizer(t, loc)

	rec, body := f.do(t, "/api/v1/instances/homelab/series-cache?lang=ru-RU")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "Rick and Morty", body.Items[0].Title)
	assert.Equal(t, 1, loc.calls)
}

// Item with nil canon SeriesID (broken/pre-cutover row) keeps canon; a
// sibling with a canon id is localized; exactly one batch call.
func TestInstancesHandler_LocalizeSeriesCacheTitles_NilCanonSkipped(t *testing.T) {
	t.Parallel()
	id11 := shareddomain.SeriesID(11)
	loc := &fakeCatalogLocalizer{titles: map[shareddomain.SeriesID]string{11: "Друзья"}}
	h := newLocalizingHandler(loc)
	items := []dto.SeriesCacheItem{
		{Title: "Broken Canon", SeriesID: nil},
		{Title: "Friends", SeriesID: &id11},
	}
	h.localizeSeriesCacheTitles(context.Background(), "ru-RU", items)
	assert.Equal(t, "Broken Canon", items[0].Title)
	assert.Equal(t, "Друзья", items[1].Title)
	assert.Equal(t, 1, loc.calls)
}

// Unwired localizer → canon titles, no panic.
func TestInstancesHandler_LocalizeSeriesCacheTitles_Unwired(t *testing.T) {
	t.Parallel()
	id := shareddomain.SeriesID(10)
	h := newLocalizingHandler(nil)
	items := []dto.SeriesCacheItem{{Title: "Canon", SeriesID: &id}}
	h.localizeSeriesCacheTitles(context.Background(), "ru-RU", items)
	assert.Equal(t, "Canon", items[0].Title)
}

// Localizer error → canon (soft-fail, no panic).
func TestInstancesHandler_LocalizeSeriesCacheTitles_ErrorSoftFail(t *testing.T) {
	t.Parallel()
	id := shareddomain.SeriesID(10)
	loc := &fakeCatalogLocalizer{err: errors.New("db down")}
	h := newLocalizingHandler(loc)
	items := []dto.SeriesCacheItem{{Title: "Canon", SeriesID: &id}}
	h.localizeSeriesCacheTitles(context.Background(), "ru-RU", items)
	assert.Equal(t, "Canon", items[0].Title, "soft-fail: canon on localizer error")
}
