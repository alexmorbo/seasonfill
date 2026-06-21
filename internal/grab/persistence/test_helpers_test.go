package persistence

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

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
func seedSonarrInstance(t *testing.T, db *gorm.DB, name domain.InstanceName) {
	t.Helper()
	const insertSQL = `INSERT INTO sonarr_instance (name, url, mode, health, transitions_count)
	                   VALUES (?, ?, ?, ?, ?)
	                   ON CONFLICT (name) DO NOTHING`
	require.NoError(t,
		db.Exec(insertSQL, string(name), "http://localhost", "auto", "unknown", 0).Error,
	)
}
