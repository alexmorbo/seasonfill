package database

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

func TestMigrate_CreatesTables(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	// New D-1 schema canonical tables (sample — full set in
	// tests/integration/d1_acceptance_test.go which enumerates all 41).
	for _, table := range []string{
		"series", "seasons", "episodes",
		"series_cache", "episode_states", "season_stats",
		"sonarr_instance", "instance_secret",
		"users", "user_instance_tags",
		"watchdog_state", "watchdog_blacklist",
		"grab_records", "episode_grabs", "download_links",
		"enrichment_errors",
	} {
		assert.True(t, db.Migrator().HasTable(table),
			"new D-1 schema must include %q", table)
	}

	// Idempotent — running twice must be a no-op.
	assert.NoError(t, Migrate(db))
}

func TestMigrate_Error(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = Migrate(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database is closed")
}

// TestMigrationFilesEmbedded asserts the embed.FS is wired correctly
// after the D-2 cutover (path is now infrastructure/database/migrations/).
func TestMigrationFilesEmbedded(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		dialect string
		fsys    fs.FS
	}{
		{"postgres", migrations.Postgres},
		{"sqlite", migrations.SQLite},
	} {
		t.Run(tc.dialect, func(t *testing.T) {
			t.Parallel()
			data, err := fs.ReadFile(tc.fsys, tc.dialect+"/000001_core_series.up.sql")
			require.NoError(t, err)
			assert.Contains(t, string(data), "CREATE TABLE",
				"core_series up.sql should contain CREATE TABLE statements")
			assert.Contains(t, string(data), "series",
				"core_series up.sql should mention series table")
		})
	}
}

// TestMigrationFilesHaveDownSibling enforces the convention that every
// up.sql ships with a paired down.sql.
func TestMigrationFilesHaveDownSibling(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		dialect string
		fsys    fs.FS
	}{
		{"postgres", migrations.Postgres},
		{"sqlite", migrations.SQLite},
	} {
		entries, err := fs.ReadDir(tc.fsys, tc.dialect)
		require.NoError(t, err)

		ups := map[string]bool{}
		downs := map[string]bool{}
		for _, e := range entries {
			name := e.Name()
			switch {
			case strings.HasSuffix(name, ".up.sql"):
				ups[strings.TrimSuffix(name, ".up.sql")] = true
			case strings.HasSuffix(name, ".down.sql"):
				downs[strings.TrimSuffix(name, ".down.sql")] = true
			}
		}
		for prefix := range ups {
			assert.True(t, downs[prefix], "missing %s", path.Join(tc.dialect, prefix+".down.sql"))
		}
		for prefix := range downs {
			assert.True(t, ups[prefix], "missing %s", path.Join(tc.dialect, prefix+".up.sql"))
		}
	}
}
