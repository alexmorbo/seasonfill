package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestOriginRelease_Upsert_Get(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedSonarrInstance(t, db, "main")
			repo := NewOriginReleaseRepository(db)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, repo.Upsert(ctx, ports.OriginRelease{
				InstanceName: "main",
				SeriesID:     122,
				SeasonNumber: 2,
				GUID:         "g1",
				IndexerName:  "RT",
				Source:       "our_grab",
				FirstSeenAt:  now,
				LastSeenAt:   now,
				LastUsedAt:   &now,
			}))

			got, ok, err := repo.Get(ctx, "main", 122, 2)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, "g1", got.GUID)
			assert.Equal(t, "our_grab", got.Source)
			assert.Equal(t, "RT", got.IndexerName)
			require.NotNil(t, got.LastUsedAt)
		})
	}
}

func TestOriginRelease_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOriginReleaseRepository(db)
			_, ok, err := repo.Get(context.Background(), "main", 999, 1)
			require.NoError(t, err)
			assert.False(t, ok)
		})
	}
}

func TestOriginRelease_Upsert_TrackingLastSeen(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedSonarrInstance(t, db, "main")
			repo := NewOriginReleaseRepository(db)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, repo.Upsert(ctx, ports.OriginRelease{
				InstanceName: "main", SeriesID: 1, SeasonNumber: 1, GUID: "first",
				Source: "our_grab", FirstSeenAt: now, LastSeenAt: now,
			}))
			later := now.Add(time.Hour)
			require.NoError(t, repo.Upsert(ctx, ports.OriginRelease{
				InstanceName: "main", SeriesID: 1, SeasonNumber: 1, GUID: "second",
				Source: "our_grab", FirstSeenAt: now, LastSeenAt: later,
			}))
			got, ok, err := repo.Get(ctx, "main", 1, 1)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, "second", got.GUID)
			// last_seen_at advances after the second upsert.
			assert.True(t, !got.LastSeenAt.Before(later) || got.LastSeenAt.Equal(later),
				"last_seen_at must advance to the new write (got %s, want >= %s)",
				got.LastSeenAt, later)
		})
	}
}

func TestOriginRelease_Upsert_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOriginReleaseRepository(db)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			err = repo.Upsert(context.Background(), ports.OriginRelease{
				InstanceName: "main", SeriesID: 1, SeasonNumber: 1, GUID: "x", Source: "our_grab",
				FirstSeenAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(),
			})
			require.Error(t, err)
		})
	}
}
