package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func sampleSettings(instanceID uint) ports.QbitSettingsRecord {
	return ports.QbitSettingsRecord{
		InstanceID:             instanceID,
		Enabled:                true,
		URL:                    "http://qbit.local:8080",
		Username:               new("admin"),
		PasswordEncrypted:      []byte{0x01, 0x02, 0x03, 0xff},
		Category:               "sonarr",
		PollIntervalMinutes:    30,
		RegrabCooldownHours:    120,
		MaxConsecutiveNoBetter: 3,
		CustomUnregisteredMsgs: []string{"Раздача погашена", "deleted"},
		PublicURL:              "https://qbit.example.com",
	}
}

func TestQbitSettingsRepository_Upsert_Insert(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings(7)
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)
			assert.Equal(t, in.InstanceID, got.InstanceID)
			assert.Equal(t, in.Enabled, got.Enabled)
			assert.Equal(t, in.URL, got.URL)
			require.NotNil(t, got.Username)
			assert.Equal(t, "admin", *got.Username)
			assert.Equal(t, in.PasswordEncrypted, got.PasswordEncrypted)
			assert.Equal(t, in.Category, got.Category)
			assert.Equal(t, in.PollIntervalMinutes, got.PollIntervalMinutes)
			assert.Equal(t, in.RegrabCooldownHours, got.RegrabCooldownHours)
			assert.Equal(t, in.MaxConsecutiveNoBetter, got.MaxConsecutiveNoBetter)
			assert.Equal(t, in.CustomUnregisteredMsgs, got.CustomUnregisteredMsgs)
			assert.Equal(t, in.PublicURL, got.PublicURL)
			assert.False(t, got.CreatedAt.IsZero())
			assert.False(t, got.UpdatedAt.IsZero())
		})
	}
}

// TestQbitSettingsRepository_Upsert_EmptyPublicURL_StoredAsNull verifies
// the marshalling: an empty PublicURL on the record should result in a
// NULL column on disk, which round-trips back as the empty string. This
// is the F-P2-1 backwards-compat invariant.
func TestQbitSettingsRepository_Upsert_EmptyPublicURL_StoredAsNull(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings(7)
			in.PublicURL = ""
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)
			assert.Equal(t, "", got.PublicURL)
		})
	}
}

// TestQbitSettingsRepository_Upsert_ClearPublicURLOnUpdate verifies that
// flipping a previously-set PublicURL back to empty round-trips as empty
// on the subsequent read.
func TestQbitSettingsRepository_Upsert_ClearPublicURLOnUpdate(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSettings(7)))
			upd := sampleSettings(7)
			upd.PublicURL = ""
			require.NoError(t, repo.Upsert(ctx, upd))

			got, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)
			assert.Equal(t, "", got.PublicURL)
		})
	}
}

func TestQbitSettingsRepository_Upsert_UpdatesOnConflict(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			first := sampleSettings(7)
			require.NoError(t, repo.Upsert(ctx, first))

			second := sampleSettings(7)
			second.Enabled = false
			second.URL = "http://qbit2.local:8080"
			second.PollIntervalMinutes = 60
			second.CustomUnregisteredMsgs = []string{"updated"}
			second.PasswordEncrypted = []byte{0xaa, 0xbb}
			require.NoError(t, repo.Upsert(ctx, second))

			got, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)
			assert.False(t, got.Enabled)
			assert.Equal(t, "http://qbit2.local:8080", got.URL)
			assert.Equal(t, 60, got.PollIntervalMinutes)
			assert.Equal(t, []string{"updated"}, got.CustomUnregisteredMsgs)
			assert.Equal(t, []byte{0xaa, 0xbb}, got.PasswordEncrypted)
		})
	}
}

func TestQbitSettingsRepository_Upsert_RejectsZeroInstanceID(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)

			in := sampleSettings(0)
			err := repo.Upsert(context.Background(), in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "instance_id")
		})
	}
}

func TestQbitSettingsRepository_Upsert_EmptyCustomMsgs(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings(7)
			in.CustomUnregisteredMsgs = nil
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)
			assert.NotNil(t, got.CustomUnregisteredMsgs)
			assert.Empty(t, got.CustomUnregisteredMsgs)
		})
	}
}

func TestQbitSettingsRepository_Upsert_NilUsername(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings(7)
			in.Username = nil
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)
			assert.Nil(t, got.Username, "nil username (no-auth local qBit) round-trips")
		})
	}
}

func TestQbitSettingsRepository_GetByInstance_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			const missing uint = 999
			_, err := repo.GetByInstance(context.Background(), missing)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))

			var typedErr *sharedErrors.QbitSettingsNotFoundError
			require.True(t, errors.As(err, &typedErr))
			assert.Equal(t, missing, typedErr.InstanceID)
		})
	}
}

func TestQbitSettingsRepository_DeleteByInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings(7)
			require.NoError(t, repo.Upsert(ctx, in))
			require.NoError(t, repo.DeleteByInstance(ctx, 7))

			_, err := repo.GetByInstance(ctx, 7)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestQbitSettingsRepository_DeleteByInstance_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			err := repo.DeleteByInstance(context.Background(), 999)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestQbitSettingsRepository_List(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSettings(7)))
			require.NoError(t, repo.Upsert(ctx, sampleSettings(8)))

			all, err := repo.List(ctx)
			require.NoError(t, err)
			assert.Len(t, all, 2)
		})
	}
}

func TestQbitSettingsRepository_List_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			all, err := repo.List(context.Background())
			require.NoError(t, err)
			assert.Empty(t, all)
		})
	}
}

func TestQbitSettingsRepository_UpsertSetsCreatedAtOnce(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSettings(7)))
			first, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)

			time.Sleep(10 * time.Millisecond)
			upd := sampleSettings(7)
			upd.CreatedAt = first.CreatedAt
			upd.Enabled = false
			require.NoError(t, repo.Upsert(ctx, upd))

			second, err := repo.GetByInstance(ctx, 7)
			require.NoError(t, err)
			assert.True(t, second.UpdatedAt.After(first.UpdatedAt) || second.UpdatedAt.Equal(first.UpdatedAt),
				"updated_at is touched on every upsert")
		})
	}
}

func TestQbitSettingsRepository_ClosedDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			err = repo.Upsert(context.Background(), sampleSettings(7))
			require.Error(t, err)
		})
	}
}
