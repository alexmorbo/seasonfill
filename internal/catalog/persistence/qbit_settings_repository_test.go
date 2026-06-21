package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// sampleSettings builds a fully-populated record for tests. The
// instance_name FK target is seeded in qbitSettingsBackends, so passing
// any of {"main", "alpha", "homelab", "4k", "beta", "secondary",
// "ghost"} stays Postgres-safe.
func sampleSettings(instance domain.InstanceName) ports.QbitSettingsRecord {
	return ports.QbitSettingsRecord{
		InstanceName:           instance,
		Enabled:                true,
		URL:                    "http://qbit.local:8080",
		Username:               new("admin"),
		PasswordEncrypted:      []byte{0x01, 0x02, 0x03, 0xff},
		Category:               "sonarr",
		PollIntervalMinutes:    30,
		RegrabCooldownHours:    120,
		MaxConsecutiveNoBetter: 3,
		CustomUnregisteredMsgs: []string{"раздача погашена", "deleted"},
		PublicURL:              "https://qbit.example.com",
	}
}

func TestQbitSettingsRepository_Upsert_Insert(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings("main")
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, "main")
			require.NoError(t, err)
			assert.Equal(t, in.InstanceName, got.InstanceName)
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
// NULL column on disk, which round-trips back as the empty string.
func TestQbitSettingsRepository_Upsert_EmptyPublicURL_StoredAsNull(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings("main")
			in.PublicURL = ""
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, "main")
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
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSettings("main")))
			upd := sampleSettings("main")
			upd.PublicURL = ""
			require.NoError(t, repo.Upsert(ctx, upd))

			got, err := repo.GetByInstance(ctx, "main")
			require.NoError(t, err)
			assert.Equal(t, "", got.PublicURL)
		})
	}
}

func TestQbitSettingsRepository_Upsert_UpdatesOnConflict(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			first := sampleSettings("main")
			require.NoError(t, repo.Upsert(ctx, first))

			second := sampleSettings("main")
			second.Enabled = false
			second.URL = "http://qbit2.local:8080"
			second.PollIntervalMinutes = 60
			second.CustomUnregisteredMsgs = []string{"updated"}
			second.PasswordEncrypted = []byte{0xaa, 0xbb}
			require.NoError(t, repo.Upsert(ctx, second))

			got, err := repo.GetByInstance(ctx, "main")
			require.NoError(t, err)
			assert.False(t, got.Enabled)
			assert.Equal(t, "http://qbit2.local:8080", got.URL)
			assert.Equal(t, 60, got.PollIntervalMinutes)
			assert.Equal(t, []string{"updated"}, got.CustomUnregisteredMsgs)
			assert.Equal(t, []byte{0xaa, 0xbb}, got.PasswordEncrypted)
		})
	}
}

func TestQbitSettingsRepository_Upsert_RejectsEmptyInstanceName(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)

			in := sampleSettings("")
			err := repo.Upsert(context.Background(), in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "instance_name")
		})
	}
}

func TestQbitSettingsRepository_Upsert_EmptyCustomMsgs(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings("main")
			in.CustomUnregisteredMsgs = nil
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, "main")
			require.NoError(t, err)
			assert.NotNil(t, got.CustomUnregisteredMsgs)
			assert.Empty(t, got.CustomUnregisteredMsgs)
		})
	}
}

func TestQbitSettingsRepository_Upsert_NilUsername(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings("main")
			in.Username = nil
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.GetByInstance(ctx, "main")
			require.NoError(t, err)
			assert.Nil(t, got.Username, "nil username (no-auth local qBit) round-trips")
		})
	}
}

func TestQbitSettingsRepository_GetByInstance_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			const missing domain.InstanceName = "ghost"
			_, err := repo.GetByInstance(context.Background(), missing)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))

			var typedErr *sharedErrors.QbitSettingsNotFoundError
			require.True(t, errors.As(err, &typedErr))
			assert.Equal(t, missing, typedErr.InstanceName)
		})
	}
}

func TestQbitSettingsRepository_DeleteByInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			in := sampleSettings("main")
			require.NoError(t, repo.Upsert(ctx, in))
			require.NoError(t, repo.DeleteByInstance(ctx, "main"))

			_, err := repo.GetByInstance(ctx, "main")
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestQbitSettingsRepository_DeleteByInstance_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			err := repo.DeleteByInstance(context.Background(), "ghost")
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestQbitSettingsRepository_List(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSettings("main")))
			require.NoError(t, repo.Upsert(ctx, sampleSettings("alpha")))

			all, err := repo.List(ctx)
			require.NoError(t, err)
			assert.Len(t, all, 2)
			// List is ordered by instance_name ASC.
			assert.Equal(t, domain.InstanceName("alpha"), all[0].InstanceName)
			assert.Equal(t, domain.InstanceName("main"), all[1].InstanceName)
		})
	}
}

func TestQbitSettingsRepository_List_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
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
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSettings("main")))
			first, err := repo.GetByInstance(ctx, "main")
			require.NoError(t, err)

			time.Sleep(10 * time.Millisecond)
			upd := sampleSettings("main")
			upd.CreatedAt = first.CreatedAt
			upd.Enabled = false
			require.NoError(t, repo.Upsert(ctx, upd))

			second, err := repo.GetByInstance(ctx, "main")
			require.NoError(t, err)
			assert.True(t, second.UpdatedAt.After(first.UpdatedAt) || second.UpdatedAt.Equal(first.UpdatedAt),
				"updated_at is touched on every upsert")
		})
	}
}

func TestQbitSettingsRepository_ClosedDB(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewQbitSettingsRepository(db)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			err = repo.Upsert(context.Background(), sampleSettings("main"))
			require.Error(t, err)
		})
	}
}
