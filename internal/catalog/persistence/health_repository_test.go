package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	discopersistence "github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// healthNow — deterministic anchor for the stale/stuck cutoffs.
var healthNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

func defaultStaleCutoffs(now time.Time) ports.StaleCutoffs {
	return ports.StaleCutoffs{
		HotBefore:    now.Add(-7 * 24 * time.Hour),
		NormalBefore: now.Add(-14 * 24 * time.Hour),
		ColdBefore:   now.Add(-30 * 24 * time.Hour),
	}
}

// seedHealthSeries inserts a series row with an explicit id and optional
// tmdb/tvdb ids and enrichment_tmdb_synced_at. Direct model Create keeps
// full control over the NULL-focused columns (no Upsert COALESCE).
func seedHealthSeries(t *testing.T, db *gorm.DB, id domain.SeriesID, tmdbID, tvdbID *int, syncedAt *time.Time) {
	t.Helper()
	row := database.SeriesModel{
		ID:                     id,
		Hydration:              "stub",
		OriginalTitle:          new(fmt.Sprintf("Series %d", id)),
		OriginCountries:        datatypes.JSON([]byte("[]")), // NOT NULL column
		EnrichmentTMDBSyncedAt: syncedAt,
		CreatedAt:              healthNow,
		UpdatedAt:              healthNow,
	}
	if tmdbID != nil {
		v := domain.TMDBID(*tmdbID)
		row.TMDBID = &v
	}
	if tvdbID != nil {
		v := domain.TVDBID(*tvdbID)
		row.TVDBID = &v
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedHealthMediaText inserts one series_media_texts row. poster nil =
// NULL poster_asset; non-nil = that value (may be "").
func seedHealthMediaText(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, lang string, poster *string) {
	t.Helper()
	row := database.SeriesMediaTextModel{
		SeriesID:    seriesID,
		Language:    lang,
		PosterAsset: poster,
		UpdatedAt:   healthNow,
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedHealthCache marks a series HOT (series_cache row, deleted_at NULL).
// Seeds the FK-target sonarr_instance first so Postgres is satisfied.
func seedHealthCache(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, sonarrID int) {
	t.Helper()
	seedSonarrInstance(t, db, "main")
	sid := seriesID
	row := database.SeriesCacheModel{
		InstanceName:   "main",
		SonarrSeriesID: domain.SonarrSeriesID(sonarrID),
		SeriesID:       &sid,
		TitleSlug:      fmt.Sprintf("slug-%d", seriesID),
		UpdatedAt:      healthNow,
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedHealthDiscovery marks a series NORMAL (discovery_lists row).
func seedHealthDiscovery(t *testing.T, db *gorm.DB, seriesID domain.SeriesID) {
	t.Helper()
	row := discopersistence.DiscoveryListsModel{
		Kind:        "popular",
		Param:       "",
		Language:    "en-US",
		SeriesID:    seriesID,
		Position:    1,
		RefreshedAt: healthNow,
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedHealthInbox inserts a webhook_inbox row with an explicit status.
func seedHealthInbox(t *testing.T, db *gorm.DB, instance, eventType, status string, attempts int, createdAt time.Time) {
	t.Helper()
	row := database.WebhookInboxModel{
		InstanceName: instance,
		EventType:    eventType,
		Payload:      []byte(`{}`),
		Status:       status,
		Attempts:     attempts,
		LastError:    "boom",
		CreatedAt:    createdAt,
	}
	require.NoError(t, db.Create(&row).Error)
}

func TestHealthRepository_MissingTVDBID(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewHealthRepository(db)
			ctx := context.Background()

			seedHealthSeries(t, db, 1, new(101), nil, nil)      // NULL tvdb → counted
			seedHealthSeries(t, db, 2, new(102), new(202), nil) // has tvdb → excluded
			seedHealthSeries(t, db, 3, new(103), nil, nil)      // NULL tvdb → counted

			count, items, err := repo.MissingTVDBID(ctx, 50)
			require.NoError(t, err)
			assert.Equal(t, 2, count)
			require.Len(t, items, 2)
			// newest id first
			assert.Equal(t, domain.SeriesID(3), items[0].SeriesID)
			assert.Equal(t, domain.SeriesID(1), items[1].SeriesID)
			assert.Equal(t, "Series 3", items[0].Title)
		})
	}
}

// TestHealthRepository_MissingPoster_AnyLangTrap encodes the F-08 /
// W18-15 S-E2/E3 trap: a series with a ru row (poster NULL) AND an en row
// WITH a poster must NOT be counted poster-less.
func TestHealthRepository_MissingPoster_AnyLangTrap(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewHealthRepository(db)
			ctx := context.Background()

			// 10: ru NULL + en SET  → has poster in SOME lang → NOT counted (THE TRAP)
			seedHealthSeries(t, db, 10, new(110), new(210), nil)
			seedHealthMediaText(t, db, 10, "ru", nil)
			seedHealthMediaText(t, db, 10, "en", new("/posters/10-en.jpg"))
			// 11: only ru NULL       → counted
			seedHealthSeries(t, db, 11, new(111), new(211), nil)
			seedHealthMediaText(t, db, 11, "ru", nil)
			// 12: no media rows      → counted
			seedHealthSeries(t, db, 12, new(112), new(212), nil)
			// 13: en empty-string    → counted (empty treated as absent)
			seedHealthSeries(t, db, 13, new(113), new(213), nil)
			seedHealthMediaText(t, db, 13, "en", new(""))
			// 14: en SET             → NOT counted
			seedHealthSeries(t, db, 14, new(114), new(214), nil)
			seedHealthMediaText(t, db, 14, "en", new("/posters/14.jpg"))

			count, items, err := repo.MissingPoster(ctx, 50)
			require.NoError(t, err)
			assert.Equal(t, 3, count, "only 11, 12, 13 are poster-less")

			got := map[domain.SeriesID]bool{}
			for _, it := range items {
				got[it.SeriesID] = true
			}
			assert.True(t, got[11] && got[12] && got[13])
			assert.False(t, got[10], "series 10 has an en poster — must NOT be poster-less (F-08)")
			assert.False(t, got[14], "series 14 has a poster — must NOT be poster-less")
		})
	}
}

func TestHealthRepository_StaleEnrichment_Tiers(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewHealthRepository(db)
			ctx := context.Background()

			d1 := healthNow.Add(-1 * time.Hour)        // fresh for all tiers
			d8 := healthNow.Add(-8 * 24 * time.Hour)   // > hot(7d), < normal(14d)
			d15 := healthNow.Add(-15 * 24 * time.Hour) // > normal(14d)
			d20 := healthNow.Add(-20 * 24 * time.Hour) // < cold(30d) → fresh cold
			d31 := healthNow.Add(-31 * 24 * time.Hour) // > cold(30d)

			// HOT fresh → excluded
			seedHealthSeries(t, db, 20, new(120), new(220), &d1)
			seedHealthCache(t, db, 20, 5020)
			// HOT stale (d8) → included, tier hot
			seedHealthSeries(t, db, 21, new(121), new(221), &d8)
			seedHealthCache(t, db, 21, 5021)
			// HOT null-sync → included, tier hot
			seedHealthSeries(t, db, 22, new(122), new(222), nil)
			seedHealthCache(t, db, 22, 5022)
			// NORMAL stale (d15) → included, tier normal
			seedHealthSeries(t, db, 23, new(123), new(223), &d15)
			seedHealthDiscovery(t, db, 23)
			// NORMAL fresh (d8 < 14d) → excluded
			seedHealthSeries(t, db, 24, new(124), new(224), &d8)
			seedHealthDiscovery(t, db, 24)
			// COLD stale (d31) → included, tier cold
			seedHealthSeries(t, db, 25, new(125), new(225), &d31)
			// COLD fresh (d20 < 30d) → excluded
			seedHealthSeries(t, db, 26, new(126), new(226), &d20)
			// no tmdb_id, null-sync → excluded (not enrichable)
			seedHealthSeries(t, db, 27, nil, new(227), nil)

			count, items, err := repo.StaleEnrichment(ctx, defaultStaleCutoffs(healthNow), 50)
			require.NoError(t, err)
			assert.Equal(t, 4, count, "21, 22, 23, 25 are stale")

			tierByID := map[domain.SeriesID]string{}
			for _, it := range items {
				tierByID[it.SeriesID] = it.Tier
			}
			require.Len(t, items, 4)
			assert.Equal(t, "hot", tierByID[21])
			assert.Equal(t, "hot", tierByID[22])
			assert.Equal(t, "normal", tierByID[23])
			assert.Equal(t, "cold", tierByID[25])
			// NULL-sync sorts first within the drill-down.
			assert.Equal(t, domain.SeriesID(22), items[0].SeriesID, "null-sync is most stale")
			assert.Nil(t, items[0].SyncedAt)
		})
	}
}

func TestHealthRepository_StuckGrabs(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewHealthRepository(db)
			ctx := context.Background()

			old := healthNow.Add(-25 * time.Hour) // stuck (>24h)
			recent := healthNow.Add(-1 * time.Hour)

			seedGrab(t, db, "main", 1, 1, grab.StatusGrabbed, old)      // counted
			seedGrab(t, db, "main", 2, 1, grab.StatusGrabbed, recent)   // within 24h → excluded
			seedGrab(t, db, "main", 3, 1, grab.StatusImported, old)     // terminal → excluded
			seedGrab(t, db, "main", 4, 1, grab.StatusGrabFailed, old)   // terminal → excluded
			seedGrab(t, db, "main", 5, 1, grab.StatusImportFailed, old) // terminal → excluded

			count, items, err := repo.StuckGrabs(ctx, healthNow.Add(-24*time.Hour), 50)
			require.NoError(t, err)
			assert.Equal(t, 1, count)
			require.Len(t, items, 1)
			assert.Equal(t, "main", string(items[0].InstanceName))
			assert.Equal(t, "Hijack", items[0].SeriesTitle)
		})
	}
}

func TestHealthRepository_DeadLetters(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewHealthRepository(db)
			ctx := context.Background()

			t0 := healthNow.Add(-3 * time.Hour)
			t1 := healthNow.Add(-1 * time.Hour)
			seedHealthInbox(t, db, "main", "Download", ports.WebhookInboxStatusDead, 6, t0)
			seedHealthInbox(t, db, "anime", "Grab", ports.WebhookInboxStatusDead, 6, t1)
			seedHealthInbox(t, db, "main", "Download", ports.WebhookInboxStatusPending, 1, t0)
			seedHealthInbox(t, db, "main", "Download", ports.WebhookInboxStatusProcessing, 2, t0)

			count, items, err := repo.DeadLetters(ctx, 50)
			require.NoError(t, err)
			assert.Equal(t, 2, count, "only status='dead' counted")
			require.Len(t, items, 2)
			// newest first
			assert.Equal(t, "anime", items[0].InstanceName)
			assert.Equal(t, "Grab", items[0].EventType)
			assert.Equal(t, 6, items[0].Attempts)
			assert.Equal(t, "boom", items[0].LastError)
		})
	}
}

// TestHealthRepository_EmptyState — the negative case: a fresh DB yields
// zero counts and empty (len 0) drill-downs for every signal.
func TestHealthRepository_EmptyState(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewHealthRepository(db)
			ctx := context.Background()

			c1, i1, err := repo.MissingTVDBID(ctx, 50)
			require.NoError(t, err)
			assert.Zero(t, c1)
			assert.Empty(t, i1)

			c2, i2, err := repo.MissingPoster(ctx, 50)
			require.NoError(t, err)
			assert.Zero(t, c2)
			assert.Empty(t, i2)

			c3, i3, err := repo.StaleEnrichment(ctx, defaultStaleCutoffs(healthNow), 50)
			require.NoError(t, err)
			assert.Zero(t, c3)
			assert.Empty(t, i3)

			c4, i4, err := repo.StuckGrabs(ctx, healthNow.Add(-24*time.Hour), 50)
			require.NoError(t, err)
			assert.Zero(t, c4)
			assert.Empty(t, i4)

			c5, i5, err := repo.DeadLetters(ctx, 50)
			require.NoError(t, err)
			assert.Zero(t, c5)
			assert.Empty(t, i5)
		})
	}
}
