package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestQbitTorrentEventsRepository_InsertStateChange(t *testing.T) {
	t.Skip("pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewQbitTorrentEventsRepository(db)
			ctx := context.Background()

			row := torrentsync.EventRow{
				Instance: "alpha", Hash: "aaaa",
				Event: torrentsync.EventStateChange,
				From:  qbit.StateGroupDownloading,
				To:    qbit.StateGroupSeeding,
				At:    time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
			}
			require.NoError(t, r.Insert(ctx, row))

			var got database.QbitTorrentEventModel
			require.NoError(t, db.First(&got).Error)
			assert.Equal(t, "state_change", got.Event)
			require.NotNil(t, got.FromGroup)
			assert.Equal(t, "downloading", *got.FromGroup)
			require.NotNil(t, got.ToGroup)
			assert.Equal(t, "seeding", *got.ToGroup)
		})
	}
}

func TestQbitTorrentEventsRepository_InsertDeletedHasNilToGroup(t *testing.T) {
	t.Skip("pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewQbitTorrentEventsRepository(db)
			ctx := context.Background()

			require.NoError(t, r.Insert(ctx, torrentsync.EventRow{
				Instance: "alpha", Hash: "aaaa",
				Event: torrentsync.EventDeleted,
				At:    time.Now().UTC(),
			}))
			var got database.QbitTorrentEventModel
			require.NoError(t, db.First(&got).Error)
			assert.Equal(t, "deleted", got.Event)
			assert.Nil(t, got.ToGroup, "deleted events leave to_group null")
		})
	}
}

// TestQbitTorrentEventsRepository_PruneOlderThan_MissingTable_Skips
// exercises the pre-A-1 skip path. Migrated from application/gc in
// story 421 (A-3 mini).
func TestQbitTorrentEventsRepository_PruneOlderThan_MissingTable_Skips(t *testing.T) {
	t.Skip("pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			// 219 (A-1) created qbit_torrent_events as part of the standard
			// migration chain; drop it to exercise the pre-A-1 skip path.
			require.NoError(t, db.Exec(`DROP TABLE IF EXISTS qbit_torrent_events`).Error)
			r := NewQbitTorrentEventsRepository(db)
			deleted, skipped, skipReason, err := r.PruneOlderThan(context.Background(), time.Now().UTC())
			require.NoError(t, err)
			assert.True(t, skipped)
			assert.Equal(t, "table_not_present_pending_a3", skipReason)
			assert.Equal(t, 0, deleted)
		})
	}
}

// TestQbitTorrentEventsRepository_PruneOlderThan_DeletesOldRows
// migrated from application/gc in story 421 (A-3 mini).
func TestQbitTorrentEventsRepository_PruneOlderThan_DeletesOldRows(t *testing.T) {
	t.Skip("pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewQbitTorrentEventsRepository(db)

			now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
			old := now.Add(-200 * 24 * time.Hour)
			fresh := now.Add(-10 * 24 * time.Hour)
			require.NoError(t, db.Exec(
				`INSERT INTO qbit_torrent_events (instance_name, torrent_hash, event, occurred_at) VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
				"inst", "h1", "added", old,
				"inst", "h2", "added", fresh,
			).Error)

			cutoff := now.Add(-180 * 24 * time.Hour)
			deleted, skipped, skipReason, err := r.PruneOlderThan(context.Background(), cutoff)
			require.NoError(t, err)
			assert.False(t, skipped)
			assert.Empty(t, skipReason)
			assert.Equal(t, 1, deleted)
		})
	}
}
