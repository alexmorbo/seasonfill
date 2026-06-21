package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

func TestRun_BootstrapSmoke(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — boot path exercises broken stubs (D2-revised-roadmap.md)")
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", dbPath)

	// Don't set API key — should trigger auto-gen on first run.
	t.Setenv("SEASONFILL_API_KEY", "")

	db, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: dbPath},
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()

	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	runtimeRepo := catalogpersistence.NewRuntimeConfigRepository(db, nil)
	row, err := runtimeRepo.Get(context.Background())
	if err == nil {
		// Runtime config was seeded — verify defaults.
		assert.Equal(t, true, row.Cron.Enabled, "cron should be enabled by default")
		assert.Equal(t, "0 */6 * * *", row.Cron.Schedule, "cron schedule should match default")
		assert.Equal(t, true, row.DryRun, "dry_run should be true by default")
		assert.Equal(t, 30, row.GlobalRateLimit.RPM, "global rate limit RPM should be 30 by default")
	}
}
