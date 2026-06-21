package persistence

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// grabBackends wraps testhelpers.AllBackends and pre-seeds the canonical
// `main` sonarr_instance row in every DB it returns. The grab + decision
// + origin_releases tables all FK→sonarr_instance(name) CASCADE; SQLite
// without `PRAGMA foreign_keys=on` lets orphan inserts slip through, but
// Postgres rejects them with constraint code 23503. Pre-seeding "main"
// (the value every grab/decision test fixture uses) makes the test
// matrix backend-portable per the D-0 quality bar.
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
				// Seed the canonical instance names every grab / decision
				// / origin_releases test fixture uses (`main`, `homelab`,
				// `4k`). FK CASCADE on delete is preserved; the OnConflict
				// DO NOTHING in seedSonarrInstance keeps repeats safe.
				seedSonarrInstance(tb, db, "main")
				seedSonarrInstance(tb, db, "homelab")
				seedSonarrInstance(tb, db, "4k")
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
