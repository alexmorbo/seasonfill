package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
	watchdogpersistence "github.com/alexmorbo/seasonfill/internal/watchdog/persistence"
)

func seedInstance(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	m := database.SonarrInstanceModel{
		Name:      name,
		URL:       "http://" + name,
		Mode:      "managed",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, db.Create(&m).Error)
	return m.ID
}

func seedOrigin(t *testing.T, db *gorm.DB, repo *OriginReleaseRepository, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, indexer string, now time.Time) {
	t.Helper()
	require.NoError(t, repo.Upsert(context.Background(), ports.OriginRelease{
		InstanceName: instance,
		SeriesID:     seriesID,
		SeasonNumber: season,
		GUID:         "g-" + string(instance),
		IndexerName:  indexer,
		Source:       "our_grab",
		FirstSeenAt:  now,
		LastSeenAt:   now,
		LastUsedAt:   &now,
	}))
}

func seedSeriesCache(t *testing.T, db *gorm.DB, repo *catalogpersistence.SeriesCacheRepository, instance domain.InstanceName, seriesID domain.SonarrSeriesID, title string, monitored bool, missing int, lastAired time.Time) {
	t.Helper()
	require.NoError(t, repo.Upsert(context.Background(), series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: seriesID,
		Title:          title,
		TitleSlug:      title,
		Monitored:      monitored,
		MissingCount:   missing,
		LastAiredAt:    &lastAired,
		UpdatedAt:      time.Now().UTC(),
	}))
}

func TestWatchdogSeasons_List_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogSeasonsRepository(db)
			rows, next, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{}, 10, nil, time.Now().UTC())
			require.NoError(t, err)
			assert.Empty(t, rows)
			assert.Nil(t, next)
		})
	}
}

func TestWatchdogSeasons_List_OriginOnly_NoSiblings(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			now := time.Now().UTC().Truncate(time.Second)

			seedInstance(t, db, "homelab")
			seedSeriesCache(t, db, scRepo, "homelab", 169, "Friends", false, 0, now)
			seedOrigin(t, db, originRepo, "homelab", 169, 2, "Prowlarr", now)

			repo := NewWatchdogSeasonsRepository(db)
			rows, next, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{}, 10, nil, now)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Nil(t, next)

			row := rows[0]
			assert.Equal(t, domain.InstanceName("homelab"), row.InstanceName)
			assert.Equal(t, domain.SonarrSeriesID(169), row.SeriesID)
			assert.Equal(t, 2, row.SeasonNumber)
			assert.Equal(t, "Prowlarr", row.OriginIndexerName)
			assert.Equal(t, "Friends", row.SeriesTitle)
			assert.False(t, row.Monitored)
			assert.Nil(t, row.Cooldown, "no cooldown row")
			assert.Nil(t, row.NoBetterCounter, "no no_better row")
			assert.Nil(t, row.Blacklist, "no blacklist row")
		})
	}
}

// Regression: origin_releases rows whose instance_name no longer
// matches any configured sonarr_instance (operator renamed/removed
// the instance) must NOT surface on /watchdog/seasons. The DB row is
// retained so the operator can clean up out-of-band.
func TestWatchdogSeasons_List_HidesRowsForUnknownInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			now := time.Now().UTC().Truncate(time.Second)

			// Configured instance + its series + its origin row.
			seedInstance(t, db, "homelab")
			seedSeriesCache(t, db, scRepo, "homelab", 369, "FROM", true, 0, now)
			seedOrigin(t, db, originRepo, "homelab", 369, 4, "Prowlarr", now)

			// Orphan origin row pointing at a phantom instance ("Sonarr"); the
			// series_cache has the same series_id under "homelab" but NOT
			// under "Sonarr" — exactly the production shape we hit.
			seedOrigin(t, db, originRepo, "Sonarr", 369, 4, "Prowlarr", now)

			repo := NewWatchdogSeasonsRepository(db)
			rows, _, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{}, 10, nil, now)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, domain.InstanceName("homelab"), rows[0].InstanceName)
			assert.Equal(t, "FROM", rows[0].SeriesTitle)
		})
	}
}

// Regression: origin_releases rows whose series_cache row is missing
// (series deleted from Sonarr, cache row never written or
// soft-deleted) must NOT surface — they render with an empty title in
// the UI which the operator reads as a corrupted row.
func TestWatchdogSeasons_List_HidesRowsForMissingSeriesCache(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			now := time.Now().UTC().Truncate(time.Second)

			seedInstance(t, db, "homelab")
			seedSeriesCache(t, db, scRepo, "homelab", 100, "The Boroughs", true, 0, now)
			seedOrigin(t, db, originRepo, "homelab", 100, 1, "Prowlarr", now)

			// series_id=999 has no series_cache row at all.
			seedOrigin(t, db, originRepo, "homelab", 999, 1, "Prowlarr", now)

			repo := NewWatchdogSeasonsRepository(db)
			rows, _, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{}, 10, nil, now)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, domain.SonarrSeriesID(100), rows[0].SeriesID)
		})
	}
}

func TestWatchdogSeasons_List_FullHierarchy(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			cdRepo := watchdogpersistence.NewCooldownRepository(db)
			nbRepo := watchdogpersistence.NewNoBetterCounterRepository(db)
			blRepo := NewWatchdogBlacklistRepository(db)
			now := time.Now().UTC().Truncate(time.Second)

			instID := seedInstance(t, db, "homelab")
			seedOrigin(t, db, originRepo, "homelab", 169, 2, "Prowlarr", now)
			seedSeriesCache(t, db, scRepo, "homelab", 169, "Friends", true, 0, now)

			// active cooldown
			require.NoError(t, cdRepo.Set(context.Background(), cooldown.Cooldown{
				Scope:     cooldown.ScopeSeries,
				Key:       cooldown.SeriesKey("homelab", 169, 2),
				ExpiresAt: now.Add(time.Hour),
				Reason:    "series_after_grab",
				CreatedAt: now,
			}))

			// no-better counter
			_, err := nbRepo.Increment(context.Background(), instID, 169, 2, now)
			require.NoError(t, err)

			// blacklist row
			bentry, err := regrab.NewBlacklistEntry(instID, 169, 2, 3, regrab.ReasonConsecutiveNoBetter, now)
			require.NoError(t, err)
			require.NoError(t, blRepo.Upsert(context.Background(), bentry))

			repo := NewWatchdogSeasonsRepository(db)
			rows, _, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{}, 10, nil, now)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			row := rows[0]
			assert.Equal(t, "Friends", row.SeriesTitle)
			assert.True(t, row.Monitored)
			require.NotNil(t, row.Cooldown)
			assert.Equal(t, "series_after_grab", row.Cooldown.Reason)
			require.NotNil(t, row.NoBetterCounter)
			assert.Equal(t, 1, row.NoBetterCounter.Consecutive)
			require.NotNil(t, row.Blacklist)
			assert.Equal(t, regrab.ReasonConsecutiveNoBetter, row.Blacklist.Reason)
		})
	}
}

func TestWatchdogSeasons_List_CooldownOnly_FiltersOut(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			cdRepo := watchdogpersistence.NewCooldownRepository(db)
			now := time.Now().UTC().Truncate(time.Second)

			seedInstance(t, db, "homelab")
			seedSeriesCache(t, db, scRepo, "homelab", 169, "Friends", true, 0, now)
			seedSeriesCache(t, db, scRepo, "homelab", 200, "ER", true, 0, now)
			seedOrigin(t, db, originRepo, "homelab", 169, 2, "Prowlarr", now)
			seedOrigin(t, db, originRepo, "homelab", 200, 1, "Prowlarr", now)

			// Only series 200 has a cooldown.
			require.NoError(t, cdRepo.Set(context.Background(), cooldown.Cooldown{
				Scope:     cooldown.ScopeSeries,
				Key:       cooldown.SeriesKey("homelab", 200, 1),
				ExpiresAt: now.Add(time.Hour),
				Reason:    "series_after_grab",
				CreatedAt: now,
			}))

			repo := NewWatchdogSeasonsRepository(db)
			rows, _, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{CooldownOnly: true}, 10, nil, now)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, domain.SonarrSeriesID(200), rows[0].SeriesID)
		})
	}
}

func TestWatchdogSeasons_List_InstanceFilter(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			now := time.Now().UTC().Truncate(time.Second)

			seedInstance(t, db, "homelab")
			seedInstance(t, db, "4k")
			seedSeriesCache(t, db, scRepo, "homelab", 169, "Friends", true, 0, now)
			seedSeriesCache(t, db, scRepo, "4k", 200, "ER", true, 0, now)
			seedOrigin(t, db, originRepo, "homelab", 169, 2, "Prowlarr", now)
			seedOrigin(t, db, originRepo, "4k", 200, 1, "Prowlarr", now)

			repo := NewWatchdogSeasonsRepository(db)
			rows, _, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{Instance: "4k"}, 10, nil, now)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, domain.InstanceName("4k"), rows[0].InstanceName)
		})
	}
}

func TestWatchdogSeasons_List_Pagination(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			now := time.Now().UTC().Truncate(time.Second)

			seedInstance(t, db, "homelab")
			seedSeriesCache(t, db, scRepo, "homelab", 100, "S100", true, 0, now)
			seedSeriesCache(t, db, scRepo, "homelab", 200, "S200", true, 0, now)
			seedSeriesCache(t, db, scRepo, "homelab", 300, "S300", true, 0, now)
			seedOrigin(t, db, originRepo, "homelab", 100, 1, "Prowlarr", now)
			seedOrigin(t, db, originRepo, "homelab", 200, 1, "Prowlarr", now)
			seedOrigin(t, db, originRepo, "homelab", 300, 1, "Prowlarr", now)

			repo := NewWatchdogSeasonsRepository(db)
			rows, next, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{}, 2, nil, now)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			require.NotNil(t, next)
			assert.Equal(t, domain.SonarrSeriesID(200), next.SeriesID)

			rows2, next2, err := repo.ListSeasons(context.Background(), WatchdogSeasonsFilter{}, 2, next, now)
			require.NoError(t, err)
			require.Len(t, rows2, 1)
			assert.Nil(t, next2)
			assert.Equal(t, domain.SonarrSeriesID(300), rows2[0].SeriesID)
		})
	}
}

func TestWatchdogSeasons_SeasonsForSeries_FromOriginAndDecisions(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			originRepo := NewOriginReleaseRepository(db)
			scRepo := catalogpersistence.NewSeriesCacheRepository(db, NewSeriesRepository(db))
			decRepo := grabpersistence.NewDecisionRepository(db)
			now := time.Now().UTC().Truncate(time.Second)

			seedInstance(t, db, "homelab")
			seedSeriesCache(t, db, scRepo, "homelab", 169, "Friends", true, 1, now)
			seedOrigin(t, db, originRepo, "homelab", 169, 2, "Prowlarr", now)

			// Season 1 has a decision but no origin row.
			d := decision.Decision{
				ID: uuid.New(), ScanRunID: uuid.New(),
				InstanceName: "homelab", SeriesID: 169, SeasonNumber: 1,
				Outcome: decision.OutcomeSkip, Reason: decision.ReasonSkipNoMissing,
				CreatedAt: now,
			}
			require.NoError(t, decRepo.Save(context.Background(), d))

			repo := NewWatchdogSeasonsRepository(db)
			rows, err := repo.SeasonsForSeries(context.Background(), "homelab", 169, now)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			assert.Equal(t, 1, rows[0].SeasonNumber)
			assert.Equal(t, 2, rows[1].SeasonNumber)
			// Both seasons share series_cache title/monitored.
			assert.Equal(t, "Friends", rows[0].SeriesTitle)
			assert.Equal(t, "Friends", rows[1].SeriesTitle)
			// Only season 2 has an origin.
			assert.Empty(t, rows[0].OriginGUID)
			assert.NotEmpty(t, rows[1].OriginGUID)
		})
	}
}

func TestWatchdogSeasons_SeasonStatsFromDecisions_Latest(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			decRepo := grabpersistence.NewDecisionRepository(db)

			scanID := uuid.New()
			older := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			newer := time.Now().UTC().Truncate(time.Second)

			// Older decision: aired=10, existing=5.
			d1 := decision.Decision{
				ID: uuid.New(), ScanRunID: scanID,
				InstanceName: "homelab", SeriesID: 169, SeasonNumber: 2,
				Outcome: decision.OutcomeSkip, Reason: decision.ReasonSkipNoMissing,
				AiredEpisodes: 10, ExistingEpisodes: 5,
				CreatedAt: older,
			}
			require.NoError(t, decRepo.Save(context.Background(), d1))

			// Newer decision: aired=10, existing=10.
			d2 := decision.Decision{
				ID: uuid.New(), ScanRunID: scanID,
				InstanceName: "homelab", SeriesID: 169, SeasonNumber: 2,
				Outcome: decision.OutcomeSkip, Reason: decision.ReasonSkipNoMissing,
				AiredEpisodes: 10, ExistingEpisodes: 10,
				CreatedAt: newer,
			}
			require.NoError(t, decRepo.Save(context.Background(), d2))

			repo := NewWatchdogSeasonsRepository(db)
			stats, err := repo.SeasonStatsFromDecisions(context.Background(), "homelab", 169)
			require.NoError(t, err)
			require.Len(t, stats, 1)
			got := stats[2]
			assert.Equal(t, 10, got.AiredEpisodes)
			assert.Equal(t, 10, got.ExistingEpisodes)
		})
	}
}

func TestWatchdogSeasons_RecentDecisions_CappedPerSeason(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			decRepo := grabpersistence.NewDecisionRepository(db)
			scanID := uuid.New()
			base := time.Now().UTC().Truncate(time.Second)

			for i := range 5 {
				d := decision.Decision{
					ID: uuid.New(), ScanRunID: scanID,
					InstanceName: "homelab", SeriesID: 169, SeasonNumber: 2,
					Outcome: decision.OutcomeSkip, Reason: decision.ReasonSkipNoMissing,
					CreatedAt: base.Add(time.Duration(i) * time.Second),
				}
				require.NoError(t, decRepo.Save(context.Background(), d))
			}

			repo := NewWatchdogSeasonsRepository(db)
			got, err := repo.RecentDecisionsBySeason(context.Background(), "homelab", 169, 3)
			require.NoError(t, err)
			require.Len(t, got[2], 3, "cap honoured per season")
			// Most recent first.
			assert.True(t, got[2][0].CreatedAt.After(got[2][1].CreatedAt))
		})
	}
}

func TestWatchdogSeasons_RecentGrabs_CappedPerSeason(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			grabRepo := grabpersistence.NewGrabRepository(db)
			base := time.Now().UTC().Truncate(time.Second)

			for i := range 4 {
				rec := domaingrab.Record{
					ID: uuid.New(), ScanRunID: uuid.New(),
					InstanceName: "homelab", SeriesID: 169, SeasonNumber: 2,
					SeriesTitle: "Friends",
					ReleaseGUID: "g", ReleaseTitle: "Severance.S03E0" + string(rune('1'+i)),
					IndexerName: "Prowlarr",
					Status:      domaingrab.StatusImported,
					CreatedAt:   base.Add(time.Duration(i) * time.Second),
					UpdatedAt:   base.Add(time.Duration(i) * time.Second),
				}
				require.NoError(t, grabRepo.Create(context.Background(), rec))
			}

			repo := NewWatchdogSeasonsRepository(db)
			got, err := repo.RecentGrabsBySeason(context.Background(), "homelab", 169, 2)
			require.NoError(t, err)
			require.Len(t, got[2], 2)
			assert.True(t, got[2][0].CreatedAt.After(got[2][1].CreatedAt))
		})
	}
}
