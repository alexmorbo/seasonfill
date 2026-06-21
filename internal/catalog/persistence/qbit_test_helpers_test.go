package persistence

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// qbitSettingsBackends wraps testhelpers.AllBackends and pre-seeds the
// sonarr_instance FK target rows that every qBit-domain fixture writes
// against — qbit_settings.instance_name, qbit_torrents.instance_name,
// qbit_torrent_events.instance_name, torrent_series_map.instance_name
// all FK→sonarr_instance.name CASCADE per migration 000018.
//
// Mirrors the pattern from internal/grab/persistence/test_helpers_test.go
// (467a). SQLite without `PRAGMA foreign_keys=on` lets orphan inserts
// slip through, but the D-0 quality bar requires the Postgres backend
// to pass too — pre-seeding the parent rows keeps both branches green
// without touching every test body.
func qbitSettingsBackends(t *testing.T) []testhelpers.Backend {
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
					"main", "homelab", "4k", "alpha", "beta", "secondary",
					"ghost", "inst",
				} {
					seedSonarrInstanceTB(tb, db, name)
				}
				return db
			},
		})
	}
	return out
}

// seedSonarrInstanceTB is the testing.TB variant of seedSonarrInstance
// (sample_helpers_test.go uses *testing.T directly). Same SQL, same
// idempotency contract — split to keep the backend-wrapper compatible
// with the testhelpers.Backend.NewDB signature, which passes
// testing.TB rather than *testing.T.
func seedSonarrInstanceTB(tb testing.TB, db *gorm.DB, name domain.InstanceName) {
	tb.Helper()
	const insertSQL = `INSERT INTO sonarr_instance (name, url, mode, health, transitions_count)
	                   VALUES (?, ?, ?, ?, ?)
	                   ON CONFLICT (name) DO NOTHING`
	require.NoError(tb,
		db.Exec(insertSQL, string(name), "http://localhost", "auto", "unknown", 0).Error,
	)
}
