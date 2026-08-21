package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/config"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/torrentaction/persistence"
)

// FindByHash is the movie twin of the series guard fallback: it resolves
// the owning instance for a hash that has no grab_records row. Misses and
// an empty hash must both surface the shared 404 shape.
func TestMovieMapRepository_FindByHash(t *testing.T) {
	gdb, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&database.TorrentMovieMapModel{}))

	require.NoError(t, gdb.Create(&database.TorrentMovieMapModel{
		InstanceName:  "alpha",
		TorrentHash:   "abcd",
		RadarrMovieID: 77,
		Source:        "webhook",
		Provenance:    "radarr_search",
		CreatedAt:     time.Now().UTC(),
	}).Error)

	repo := persistence.NewMovieMapRepository(gdb)
	ctx := context.Background()

	ref, err := repo.FindByHash(ctx, "  ABCD  ")
	require.NoError(t, err, "hash is normalised (trim + lowercase) before lookup")
	assert.Equal(t, "alpha", string(ref.InstanceName))
	assert.Equal(t, 77, int(ref.RadarrMovieID))

	_, err = repo.FindByHash(ctx, "deadbeef")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)

	_, err = repo.FindByHash(ctx, "   ")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}
