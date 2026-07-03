package persistence

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// grabBackends wraps testhelpers.AllBackends and pre-seeds the canonical
// FK targets every grab/decision test fixture references:
//
//   - sonarr_instance rows for "main", "homelab", and "4k" (the
//     instance_name values fixtures hard-code).
//   - A scan_runs row whose id is the all-zeros UUID. Decision tests
//     emit a `uuid.New()` ScanRunID by default; the
//     decisions_scan_run_id_fkey constraint (SET NULL on delete) makes
//     a random non-existing uuid an FK violation on Postgres. The
//     fixtures don't observe the scan_run row contents — they just need
//     a valid parent — so a single seeded uuid covers the matrix.
//
// SQLite without `PRAGMA foreign_keys=on` lets orphan inserts slip
// through, but the D-0 quality bar requires the Postgres backend to
// pass too. Pre-seeding the parent rows keeps both branches green
// without touching every existing test body.
func grabBackends(t *testing.T) []testhelpers.Backend {
	t.Helper()
	src := testhelpers.AllBackends(t)
	out := make([]testhelpers.Backend, 0, len(src))
	for _, b := range src {
		name := b.Name
		newDB := b.NewDB
		out = append(out, testhelpers.Backend{
			Name: name,
			NewDB: func(tb testing.TB) *gorm.DB {
				db := newDB(tb)
				for _, name := range []domain.InstanceName{
					"main", "homelab", "4k", "alpha", "beta", "a",
					"secondary", "ghost",
				} {
					seedSonarrInstance(tb, db, name)
				}
				return db
			},
		})
	}
	return out
}

// seedSonarrInstance is the idempotent FK-target helper for grab
// persistence tests that write to grab_records / decisions /
// origin_releases / download_links — every D-6 audit table whose FK
// targets sonarr_instance.name. Uses ON CONFLICT DO NOTHING so multiple
// callers within the same test don't trip the unique constraint.
//
// Mirrors the catalog/persistence/sample_helpers_test.go helper —
// dialect-portable raw SQL covering only the NOT NULL columns without
// DEFAULTs (name, url, mode, health, transitions_count); the rest use
// their migration DEFAULT clauses.
//
// SQLite without `PRAGMA foreign_keys=on` lets the orphan slip through,
// but the D-0 quality bar (dual-backend matrix) requires Postgres to
// pass too — seeding the parent row keeps both branches green.
func seedSonarrInstance(tb testing.TB, db *gorm.DB, name domain.InstanceName) {
	tb.Helper()
	const insertSQL = `INSERT INTO sonarr_instance (name, url, mode, health, transitions_count)
	                   VALUES (?, ?, ?, ?, ?)
	                   ON CONFLICT (name) DO NOTHING`
	require.NoError(tb,
		db.Exec(insertSQL, string(name), "http://localhost", "auto", "unknown", 0).Error,
	)
}

// seedSeries inserts a minimal series row so FKs into the canonical
// catalog (download_links.global_series_id, season_stats.series_id,
// etc.) can be satisfied on Postgres without a full enrichment fixture.
// ON CONFLICT DO NOTHING keeps the call idempotent.
func seedSeries(tb testing.TB, db *gorm.DB, id int64, title string) {
	tb.Helper()
	const insertSQL = `INSERT INTO series (id, original_title) VALUES (?, ?) ON CONFLICT (id) DO NOTHING`
	require.NoError(tb, db.Exec(insertSQL, id, title).Error)
}
