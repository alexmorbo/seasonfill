package persistence

// Test helpers carried over from infrastructure/database/repositories/
// sample_helpers_test.go when the catalog repository slice graduated
// to internal/catalog/persistence (story 443 A-1-17). The catalog test
// suite seeds canon series + people via the moved enrichment-persistence
// constructors. Keeping the shorter helper signatures + sample fixtures
// available here means each test still calls NewSeriesRepository(db) /
// sampleCanon("...") with the same shape it used pre-move.
//
// Future story will collapse the enrichpersistence aliases into a
// shared testhelpers package when the model split lands.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// sampleCanon mirrors the persistence-package fixture used by every
// catalog repo test. Values are identical (TMDB=101 / TVDB=202 /
// IMDB=tt0000001) to the legacy infrastructure/database/repositories
// sampleCanon so a test that upserts a canon and then asks the moved
// SeriesRepository for it gets the same row.
func sampleCanon(title string) series.Canon {
	return series.Canon{
		Title:         title,
		Hydration:     series.HydrationStub,
		TMDBID:        ptrTMDBID(101),
		TVDBID:        ptrTVDBID(202),
		IMDBID:        ptrIMDBID("tt0000001"),
		OriginalTitle: new("orig: " + title),
		Status:        new("Returning Series"),
		Year:          new(2024),
		InProduction:  true,
	}
}

// Aliases so catalog tests keep their `NewSeriesRepository(db)` call
// sites unchanged. These resolve to the moved constructors in
// internal/enrichment/persistence.
var (
	NewSeriesRepository   = enrichpersistence.NewSeriesRepository
	NewEpisodesRepository = enrichpersistence.NewEpisodesRepository
	NewNetworksRepository = enrichpersistence.NewNetworksRepository
)

// Type aliases so a catalog test can keep `*SeriesRepository` shape
// annotations and `_ var = (*SeriesRepository)(nil)` assertions
// running without import churn.
type (
	SeriesRepository   = enrichpersistence.SeriesRepository
	EpisodesRepository = enrichpersistence.EpisodesRepository
	NetworksRepository = enrichpersistence.NetworksRepository
)

// ptrTMDBID / ptrIMDBID / ptrTVDBID are thin pointer-to-typed-ID
// shorthands used by the catalog series_cache / episode_states /
// season_stats tests. They mirror the helpers in
// internal/enrichment/persistence/grab_test_helpers_test.go verbatim
// so the catalog tests don't need to reach into that package.
func ptrTMDBID(i int) *domain.TMDBID {
	v := domain.TMDBID(i)
	return &v
}

func ptrIMDBID(s string) *domain.IMDBID {
	v := domain.IMDBID(s)
	return &v
}

func ptrTVDBID(i int) *domain.TVDBID {
	v := domain.TVDBID(i)
	return &v
}

// _ keeps the `domain` import alive when this file is the only
// consumer in the catalog test set; `series` is already referenced by
// sampleCanon's return type.
var _ = domain.SeriesID(0)

// seedGrab inserts a minimal grab record via the production
// internal/grab/persistence.GrabRepository.Create path so the catalog
// tests still exercise the real write codepath (no shortcut through
// raw gorm). Mirrors the helper in
// internal/enrichment/persistence/grab_test_helpers_test.go verbatim
// so counter_repository_test can build buckets without reaching into
// the enrichment package.
//
// Seeds the parent sonarr_instance row + a parent scan_run row before
// the grab insert so the grab_records_instance_name_fkey AND
// grab_records_scan_run_id_fkey constraints are satisfied on Postgres
// (SQLite without `PRAGMA foreign_keys=on` lets the orphan slip
// through, but the production catalog tests target both backends per
// D-0). The seeded scan_run id is returned via grab.Record.ScanRunID.
func seedGrab(t *testing.T, db *gorm.DB, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, status grab.Status, createdAt time.Time) grab.Record {
	t.Helper()
	seedSonarrInstance(t, db, instance)
	scanRunID := seedScanRun(t, db, instance)
	rec := grab.Record{
		ID:           uuid.New(),
		InstanceName: instance,
		SeriesID:     seriesID,
		SeriesTitle:  "Hijack",
		SeasonNumber: season,
		ReleaseGUID:  uuid.NewString(),
		ReleaseTitle: "S02 Pack",
		IndexerID:    3,
		IndexerName:  "RT",
		Status:       status,
		ScanRunID:    scanRunID,
		Attempts:     1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	require.NoError(t, grabpersistence.NewGrabRepository(db).Create(context.Background(), rec))
	return rec
}

// seedSonarrInstance is the idempotent FK-target helper for catalog
// persistence tests that write to grab_records (and any other table
// whose FK targets sonarr_instance.name). Uses ON CONFLICT DO NOTHING
// so multiple callers within the same test don't trip the unique
// constraint.
//
// Writes raw SQL against the D-1 sonarr_instance schema (10 columns
// per 000010_admin.up.sql) rather than the legacy SonarrInstanceModel
// which carries pre-D-1 columns no longer in the table. The SQL is
// dialect-portable for SQLite + Postgres because both honor `ON
// CONFLICT (name) DO NOTHING` and both auto-fill `created_at` /
// `updated_at` from their DEFAULT clauses.
func seedSonarrInstance(t *testing.T, db *gorm.DB, name domain.InstanceName) {
	t.Helper()
	const insertSQL = `INSERT INTO sonarr_instance (name, url, mode, health, transitions_count)
	                   VALUES (?, ?, ?, ?, ?)
	                   ON CONFLICT (name) DO NOTHING`
	require.NoError(t,
		db.Exec(insertSQL, string(name), "http://localhost", "auto", "unknown", 0).Error,
	)
}

// seedScanRun inserts a minimal scan_runs row matching the D-4 schema
// (story 465b migration 000015) so direct grab_records inserts can
// satisfy grab_records_scan_run_id_fkey on Postgres. Returns the row's
// uuid so the caller can pass it as grab.Record.ScanRunID.
//
// Writes raw SQL covering only the NOT NULL columns without DEFAULTs
// (id, instance_name, trigger, started_at); the other 11 columns use
// their migration DEFAULT clauses. Dialect-portable for SQLite +
// Postgres — both accept positional placeholders + CURRENT_TIMESTAMP
// vs now().
func seedScanRun(t *testing.T, db *gorm.DB, instance domain.InstanceName) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const insertSQL = `INSERT INTO scan_runs (id, instance_name, trigger, started_at)
	                   VALUES (?, ?, ?, ?)`
	require.NoError(t,
		db.Exec(insertSQL, id.String(), string(instance), "test", time.Now().UTC()).Error,
	)
	return id
}
