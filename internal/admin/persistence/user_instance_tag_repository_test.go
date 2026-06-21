package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedTagDeps inserts the parent rows the user_instance_tags FKs
// require: one user (id=1) and one sonarr_instance ("main") so the
// CASCADE FKs resolve. The sonarr_instance insert uses raw SQL because
// the GORM SonarrInstanceModel struct still carries legacy pre-D-6
// columns (TimeoutSeconds etc.) that the new D-1 schema drops — until
// D-6 catalog rewrite shrinks the model, GORM Create against the new
// schema fails on unknown columns.
func seedTagDeps(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         1,
		Username:   "alice",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO sonarr_instance (name, url, mode, health, transitions_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"main", "http://sonarr.local", "auto", "unknown", 0, now, now,
	).Error)
}

func TestUserInstanceTagRepository_Get_NoRow_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedTagDeps(t, db)
			repo := NewUserInstanceTagRepository(db)

			_, err := repo.Get(context.Background(), 1, domain.InstanceName("main"))
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestUserInstanceTagRepository_Upsert_Then_Get(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedTagDeps(t, db)
			repo := NewUserInstanceTagRepository(db)
			ctx := context.Background()

			tag := admin.UserInstanceTag{
				UserID:         1,
				InstanceName:   domain.InstanceName("main"),
				SonarrTagID:    42,
				SonarrTagLabel: "sf-alice",
			}
			require.NoError(t, repo.Upsert(ctx, tag))

			got, err := repo.Get(ctx, 1, domain.InstanceName("main"))
			require.NoError(t, err)
			assert.Equal(t, uint(1), got.UserID)
			assert.Equal(t, domain.InstanceName("main"), got.InstanceName)
			assert.Equal(t, 42, got.SonarrTagID)
			assert.Equal(t, "sf-alice", got.SonarrTagLabel)
			assert.False(t, got.CreatedAt.IsZero())
			assert.False(t, got.UpdatedAt.IsZero())
		})
	}
}

// TestUserInstanceTagRepository_Upsert_Idempotent confirms re-calling
// Upsert with the same (user_id, instance_name) updates the tag id +
// label in place without an INSERT collision.
func TestUserInstanceTagRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedTagDeps(t, db)
			repo := NewUserInstanceTagRepository(db)
			ctx := context.Background()

			first := admin.UserInstanceTag{
				UserID: 1, InstanceName: domain.InstanceName("main"),
				SonarrTagID: 1, SonarrTagLabel: "sf-alice-v1",
			}
			require.NoError(t, repo.Upsert(ctx, first))

			second := admin.UserInstanceTag{
				UserID: 1, InstanceName: domain.InstanceName("main"),
				SonarrTagID: 99, SonarrTagLabel: "sf-alice-v2",
			}
			require.NoError(t, repo.Upsert(ctx, second))

			got, err := repo.Get(ctx, 1, domain.InstanceName("main"))
			require.NoError(t, err)
			assert.Equal(t, 99, got.SonarrTagID, "Upsert must replace the existing tag id")
			assert.Equal(t, "sf-alice-v2", got.SonarrTagLabel, "Upsert must replace the existing label")
		})
	}
}

// TestUserInstanceTagRepository_DeleteByUser_Idempotent confirms
// DeleteByUser removes all rows for a user and is idempotent for users
// with no rows.
func TestUserInstanceTagRepository_DeleteByUser_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedTagDeps(t, db)
			repo := NewUserInstanceTagRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, admin.UserInstanceTag{
				UserID: 1, InstanceName: domain.InstanceName("main"),
				SonarrTagID: 1, SonarrTagLabel: "sf-alice",
			}))

			// Sanity: row present before delete.
			_, err := repo.Get(ctx, 1, domain.InstanceName("main"))
			require.NoError(t, err)

			require.NoError(t, repo.DeleteByUser(ctx, 1))

			_, err = repo.Get(ctx, 1, domain.InstanceName("main"))
			require.Error(t, err)
			require.True(t, errors.Is(err, ports.ErrNotFound))

			// Idempotent: deleting again is a no-op.
			require.NoError(t, repo.DeleteByUser(ctx, 1))
			// Deleting an unknown user is also a no-op.
			require.NoError(t, repo.DeleteByUser(ctx, 9999))
		})
	}
}
