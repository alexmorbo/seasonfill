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
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// agentOwnerID is the seed-admin owner used by the per-user notification_agents
// tests (Ф8-U-5). seedAgentUser inserts the matching users row so the FK holds.
const agentOwnerID int64 = 1

// secondUserID is a non-admin user used to prove per-user isolation in the
// notification repos (Ф8-U-5c).
const secondUserID int64 = 2

func seedAgentUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(agentOwnerID),
		Username:   "admin",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

func seedSecondUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(secondUserID),
		Username:   "bob",
		Role:       admin.RoleUser,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

func TestAgentRepository_CreateGet_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			seedAgentUser(t, db)
			ctx := context.Background()

			cfg := []byte{0x01, 0x02, 0x03}
			id, err := repo.Create(ctx, agentOwnerID, ports.NotificationAgent{
				Name: "tg", Enabled: true, ConfigEncrypted: cfg,
				EventTypes: []string{"grab.failed", "import.failed"},
			})
			require.NoError(t, err)
			require.NotZero(t, id)

			got, err := repo.Get(ctx, id, agentOwnerID)
			require.NoError(t, err)
			assert.Equal(t, "tg", got.Name)
			assert.True(t, got.Enabled)
			assert.Equal(t, cfg, got.ConfigEncrypted)
			assert.Equal(t, []string{"grab.failed", "import.failed"}, got.EventTypes)
		})
	}
}

func TestAgentRepository_Create_RequiresConfig(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			seedAgentUser(t, db)
			_, err := repo.Create(context.Background(), agentOwnerID, ports.NotificationAgent{Name: "x", EventTypes: []string{"grab.ok"}})
			assert.Error(t, err)
		})
	}
}

func TestAgentRepository_Update_KeepOrReplaceConfig(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			seedAgentUser(t, db)
			ctx := context.Background()

			orig := []byte{0xAA, 0xBB}
			id, err := repo.Create(ctx, agentOwnerID, ports.NotificationAgent{Name: "a", Enabled: false, ConfigEncrypted: orig, EventTypes: []string{"grab.ok"}})
			require.NoError(t, err)

			// newConfig=nil → keep ciphertext; name/enabled/event_types replaced.
			require.NoError(t, repo.Update(ctx, id, agentOwnerID, "a2", true, []string{"grab.failed"}, nil))
			got, err := repo.Get(ctx, id, agentOwnerID)
			require.NoError(t, err)
			assert.Equal(t, "a2", got.Name)
			assert.True(t, got.Enabled)
			assert.Equal(t, orig, got.ConfigEncrypted)
			assert.Equal(t, []string{"grab.failed"}, got.EventTypes)

			// newConfig non-nil → replace ciphertext.
			repl := []byte{0xCC}
			require.NoError(t, repo.Update(ctx, id, agentOwnerID, "a2", true, []string{"grab.failed"}, repl))
			got, err = repo.Get(ctx, id, agentOwnerID)
			require.NoError(t, err)
			assert.Equal(t, repl, got.ConfigEncrypted)
		})
	}
}

func TestAgentRepository_ListEnabledForEventAndUser(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			seedAgentUser(t, db)
			seedSecondUser(t, db)
			ctx := context.Background()

			// user 1, enabled + subscribed
			_, err := repo.Create(ctx, agentOwnerID, ports.NotificationAgent{Name: "yes", Enabled: true, ConfigEncrypted: []byte{1}, EventTypes: []string{"grab.failed", "import.failed"}})
			require.NoError(t, err)
			// user 1, disabled + subscribed
			_, err = repo.Create(ctx, agentOwnerID, ports.NotificationAgent{Name: "disabled", Enabled: false, ConfigEncrypted: []byte{2}, EventTypes: []string{"grab.failed"}})
			require.NoError(t, err)
			// user 1, enabled + not subscribed
			_, err = repo.Create(ctx, agentOwnerID, ports.NotificationAgent{Name: "other", Enabled: true, ConfigEncrypted: []byte{3}, EventTypes: []string{"grab.ok"}})
			require.NoError(t, err)
			// user 2, enabled + subscribed → must NOT leak into user 1's result.
			_, err = repo.Create(ctx, secondUserID, ports.NotificationAgent{Name: "u2", Enabled: true, ConfigEncrypted: []byte{4}, EventTypes: []string{"grab.failed"}})
			require.NoError(t, err)

			got, err := repo.ListEnabledForEventAndUser(ctx, "grab.failed", agentOwnerID)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "yes", got[0].Name)

			// user 2 sees only its own enabled+subscribed agent.
			got2, err := repo.ListEnabledForEventAndUser(ctx, "grab.failed", secondUserID)
			require.NoError(t, err)
			require.Len(t, got2, 1)
			assert.Equal(t, "u2", got2[0].Name)
		})
	}
}

func TestAgentRepository_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			seedAgentUser(t, db)
			ctx := context.Background()

			_, err := repo.Get(ctx, 999, agentOwnerID)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
			assert.True(t, errors.Is(repo.Delete(ctx, 999, agentOwnerID), ports.ErrNotFound))
			assert.True(t, errors.Is(repo.Update(ctx, 999, agentOwnerID, "x", true, nil, nil), ports.ErrNotFound))

			// Delete existing → ok.
			id, err := repo.Create(ctx, agentOwnerID, ports.NotificationAgent{Name: "d", ConfigEncrypted: []byte{9}, EventTypes: []string{"grab.ok"}})
			require.NoError(t, err)
			require.NoError(t, repo.Delete(ctx, id, agentOwnerID))
			_, err = repo.Get(ctx, id, agentOwnerID)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestAgentRepository_OwnerScoping(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			seedAgentUser(t, db)  // id=1, owner A (admin)
			seedSecondUser(t, db) // id=2, owner B (bob, non-admin)
			ctx := context.Background()

			idA, err := repo.Create(ctx, agentOwnerID, ports.NotificationAgent{
				Name: "a-agent", Enabled: true, ConfigEncrypted: []byte{0x01},
				EventTypes: []string{"grab.failed"},
			})
			require.NoError(t, err)
			idB, err := repo.Create(ctx, secondUserID, ports.NotificationAgent{
				Name: "b-agent", Enabled: true, ConfigEncrypted: []byte{0x02},
				EventTypes: []string{"grab.failed"},
			})
			require.NoError(t, err)

			// B cannot Get A's agent → ErrNotFound (existence hidden).
			_, err = repo.Get(ctx, idA, secondUserID)
			assert.True(t, errors.Is(err, ports.ErrNotFound))

			// B cannot Update A's agent; A's row is untouched.
			err = repo.Update(ctx, idA, secondUserID, "hacked", false, []string{"grab.ok"}, []byte{0xFF})
			assert.True(t, errors.Is(err, ports.ErrNotFound))
			stillA, err := repo.Get(ctx, idA, agentOwnerID)
			require.NoError(t, err)
			assert.Equal(t, "a-agent", stillA.Name)
			assert.True(t, stillA.Enabled)

			// B cannot Delete A's agent; A's row survives.
			err = repo.Delete(ctx, idA, secondUserID)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
			_, err = repo.Get(ctx, idA, agentOwnerID)
			require.NoError(t, err)

			// ListByOwner is strictly per-owner.
			listA, err := repo.ListByOwner(ctx, agentOwnerID)
			require.NoError(t, err)
			require.Len(t, listA, 1)
			assert.Equal(t, idA, listA[0].ID)
			listB, err := repo.ListByOwner(ctx, secondUserID)
			require.NoError(t, err)
			require.Len(t, listB, 1)
			assert.Equal(t, idB, listB[0].ID)

			// Owner A operates on their own agent successfully.
			require.NoError(t, repo.Update(ctx, idA, agentOwnerID, "a2", false, []string{"grab.ok"}, nil))
			require.NoError(t, repo.Delete(ctx, idA, agentOwnerID))
			_, err = repo.Get(ctx, idA, agentOwnerID)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}
