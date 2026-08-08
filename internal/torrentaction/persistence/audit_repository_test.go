package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/config"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	appta "github.com/alexmorbo/seasonfill/internal/torrentaction/app"
	"github.com/alexmorbo/seasonfill/internal/torrentaction/persistence"
)

func TestAuditRepository_Write_RoundTrip(t *testing.T) {
	gdb, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)

	// AutoMigrate just the audit model — the table has no FK, so this is
	// sufficient for a repo unit test (full migration exercised by the
	// 000044 roundtrip test).
	require.NoError(t, gdb.AutoMigrate(&database.TorrentActionAuditModel{}))

	repo := persistence.NewAuditRepository(gdb)
	rec := appta.AuditRecord{
		InstanceName: "main",
		Hash:         "cccccccccccccccccccccccccccccccccccccccc",
		Action:       appta.ActionPause,
		Actor:        "op",
		Result:       "ok",
		CreatedAt:    time.Now().UTC(),
	}
	require.NoError(t, repo.Write(context.Background(), rec))

	var got database.TorrentActionAuditModel
	require.NoError(t, gdb.First(&got).Error)
	assert.Equal(t, "main", got.InstanceName)
	assert.Equal(t, "pause", got.Action)
	assert.Equal(t, "ok", got.Result)
	assert.NotZero(t, got.ID)
}
