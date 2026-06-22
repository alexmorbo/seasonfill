package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// D-0 quality bar (project_seasonfill_test_quality_bar):
//   - testcontainers Postgres + SQLite via testhelpers.AllBackends
//   - error-pair coverage (NotFound) alongside success
//   - t.Parallel() at every level

func TestAdminUserRepo_GetEmpty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			_, err := repo.Get(context.Background())
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr),
				"Get on empty table must surface typed UserNotFoundError via errors.As")
			require.True(t, errors.Is(err, ports.ErrNotFound),
				"Get on empty table must satisfy errors.Is(ports.ErrNotFound)")
		})
	}
}

func TestAdminUserRepo_CreateThenGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "admin",
				PasswordHash: "hashed",
			}))

			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, "admin", got.Username)
			assert.Equal(t, "hashed", got.PasswordHash)
			assert.Equal(t, admin.RoleAdmin, got.Role)
			assert.Equal(t, admin.AvatarModeAuto, got.AvatarMode)
			assert.False(t, got.CreatedAt.IsZero())
			assert.False(t, got.UpdatedAt.IsZero())
		})
	}
}

func TestAdminUserRepo_UpdatePassword(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "admin",
				PasswordHash: "old",
			}))
			row, err := repo.Get(ctx)
			require.NoError(t, err)

			require.NoError(t, repo.UpdatePassword(ctx, row.ID, "new-hash"))

			updated, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, "new-hash", updated.PasswordHash)
			assert.True(t, updated.UpdatedAt.After(row.UpdatedAt) || updated.UpdatedAt.Equal(row.UpdatedAt))
		})
	}
}

func TestAdminUserRepo_UpdatePassword_NoRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			err := repo.UpdatePassword(context.Background(), 9999, "doesntmatter")
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestAdminUserRepo_GetByOIDCSubject_Found(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			created, err := repo.CreateFromOIDC(ctx, "sub-123", "alice", "alice@example.com")
			require.NoError(t, err)
			require.NotZero(t, created.ID)

			got, err := repo.GetByOIDCSubject(ctx, "sub-123")
			require.NoError(t, err)
			assert.Equal(t, "alice", got.Username)
			require.NotNil(t, got.OIDCSubject)
			assert.Equal(t, "sub-123", *got.OIDCSubject)
			require.NotNil(t, got.Email)
			assert.Equal(t, "alice@example.com", *got.Email)
		})
	}
}

func TestAdminUserRepo_GetByOIDCSubject_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			_, err := repo.GetByOIDCSubject(context.Background(), "missing-subject")
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestAdminUserRepo_CreateFromOIDC_PopulatesSubject(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			created, err := repo.CreateFromOIDC(ctx, "subject-x", "bob", "")
			require.NoError(t, err)
			require.NotNil(t, created.OIDCSubject)
			assert.Equal(t, "subject-x", *created.OIDCSubject)
			assert.Equal(t, admin.RoleAdmin, created.Role)
			assert.Equal(t, admin.AvatarModeAuto, created.AvatarMode)
			assert.Nil(t, created.Email, "blank email arg leaves email NULL")
			assert.Empty(t, created.PasswordHash, "OIDC user has no password hash")
		})
	}
}

func TestAdminUserRepo_CreateFromOIDC_MultipleUsers(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			u1, err := repo.CreateFromOIDC(ctx, "sub-1", "alice", "")
			require.NoError(t, err)
			u2, err := repo.CreateFromOIDC(ctx, "sub-2", "bob", "")
			require.NoError(t, err)

			assert.NotEqual(t, u1.ID, u2.ID,
				"distinct subjects must yield distinct rows (multi-row capable)")

			// Get returns the first row by id ASC — alice was created first.
			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, "alice", got.Username)
		})
	}
}

// TestUserRepo_Create_DefaultRoleAvatarMode covers the 466a §Code
// default-population contract: a bare Create that leaves Role +
// AvatarMode zero MUST persist 'admin' / 'auto'.
func TestUserRepo_Create_DefaultRoleAvatarMode(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			// Bare Create — Role + AvatarMode left zero.
			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "defaulted",
				PasswordHash: "hash",
			}))

			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, admin.RoleAdmin, got.Role)
			assert.Equal(t, admin.AvatarModeAuto, got.AvatarMode)
		})
	}
}

// TestUserRepo_Create_PreferredLanguageNil confirms NULL persists as
// NULL and NOT as empty string on both backends.
func TestUserRepo_Create_PreferredLanguageNil(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "nolang",
				PasswordHash: "hash",
				// PreferredLanguage left nil.
			}))
			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Nil(t, got.PreferredLanguage,
				"NULL preferred_language must round-trip as nil pointer (not empty string)")
		})
	}
}

// TestUserRepo_UpdateLastLoginAt_Stamps covers the 466a new method:
// UpdateLastLoginAt sets the column and Get reads it back.
func TestUserRepo_UpdateLastLoginAt_Stamps(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "stamped",
				PasswordHash: "hash",
			}))
			row, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Nil(t, row.LastLoginAt, "new row → last_login_at must be NULL")

			when := time.Date(2026, 6, 21, 12, 30, 0, 0, time.UTC)
			require.NoError(t, repo.UpdateLastLoginAt(ctx, row.ID, when))

			updated, err := repo.Get(ctx)
			require.NoError(t, err)
			require.NotNil(t, updated.LastLoginAt)
			assert.WithinDuration(t, when, *updated.LastLoginAt, time.Second)
		})
	}
}

// TestUserRepo_UpdateLastLoginAt_NoRow_ErrNotFound covers the error-pair
// path on the new method.
func TestUserRepo_UpdateLastLoginAt_NoRow_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			err := repo.UpdateLastLoginAt(context.Background(), 9999, time.Now())
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

// TestUserRepo_GetByUsername covers the N-7a per-username lookup path
// used by the MeHandler to resolve the cookie-authenticated user.
func TestUserRepo_GetByUsername(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "alice",
				PasswordHash: "hash-a",
			}))
			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "bob",
				PasswordHash: "hash-b",
			}))

			gotAlice, err := repo.GetByUsername(ctx, "alice")
			require.NoError(t, err)
			assert.Equal(t, "alice", gotAlice.Username)
			assert.Equal(t, "hash-a", gotAlice.PasswordHash)

			gotBob, err := repo.GetByUsername(ctx, "bob")
			require.NoError(t, err)
			assert.Equal(t, "bob", gotBob.Username)

			_, err = repo.GetByUsername(ctx, "missing")
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

// TestUserRepo_UpdateSettings covers the N-7a partial settings patch
// (avatar_mode + preferred_language). The no-op short-circuit, the
// success path on both columns, and the typed-NotFound miss are all
// asserted.
func TestUserRepo_UpdateSettings(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "alice",
				PasswordHash: "hash-a",
			}))
			created, err := repo.GetByUsername(ctx, "alice")
			require.NoError(t, err)

			ru := "ru"
			gravatar := admin.AvatarModeGravatar
			require.NoError(t, repo.UpdateSettings(ctx, created.ID, ports.UserSettingsPatch{
				AvatarMode:        &gravatar,
				PreferredLanguage: &ru,
			}))
			after, err := repo.GetByUsername(ctx, "alice")
			require.NoError(t, err)
			assert.Equal(t, "gravatar", after.AvatarMode)
			require.NotNil(t, after.PreferredLanguage)
			assert.Equal(t, "ru", *after.PreferredLanguage)
			assert.True(t, after.UpdatedAt.After(created.UpdatedAt) ||
				after.UpdatedAt.Equal(created.UpdatedAt))

			// Empty patch is a no-op (no DB row needed).
			require.NoError(t, repo.UpdateSettings(ctx, created.ID, ports.UserSettingsPatch{}))

			// Unknown id surfaces typed not-found.
			err = repo.UpdateSettings(ctx, 9999, ports.UserSettingsPatch{
				AvatarMode: &gravatar,
			})
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

// TestUserRepo_OIDCSubject_PartialUnique covers the partial UNIQUE
// invariant on oidc_subject: NULL collisions allowed, non-NULL
// collisions rejected.
func TestUserRepo_OIDCSubject_PartialUnique(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			// Two rows without an OIDC subject (NULL) MUST coexist.
			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "forms-1",
				PasswordHash: "hash-1",
			}))
			require.NoError(t, repo.Create(ctx, admin.User{
				Username:     "forms-2",
				PasswordHash: "hash-2",
			}))

			// Two rows with the SAME non-NULL OIDC subject MUST collide.
			_, err := repo.CreateFromOIDC(ctx, "sub-collide", "user1", "")
			require.NoError(t, err)
			_, err = repo.CreateFromOIDC(ctx, "sub-collide", "user2", "")
			require.Error(t, err,
				"second CreateFromOIDC with same subject must surface the partial UNIQUE collision")
		})
	}
}
