package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestAgentRepository_CreateGet_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			ctx := context.Background()

			cfg := []byte{0x01, 0x02, 0x03}
			id, err := repo.Create(ctx, ports.NotificationAgent{
				Name: "tg", Enabled: true, ConfigEncrypted: cfg,
				EventTypes: []string{"grab.failed", "import.failed"},
			})
			require.NoError(t, err)
			require.NotZero(t, id)

			got, err := repo.Get(ctx, id)
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
			_, err := repo.Create(context.Background(), ports.NotificationAgent{Name: "x", EventTypes: []string{"grab.ok"}})
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
			ctx := context.Background()

			orig := []byte{0xAA, 0xBB}
			id, err := repo.Create(ctx, ports.NotificationAgent{Name: "a", Enabled: false, ConfigEncrypted: orig, EventTypes: []string{"grab.ok"}})
			require.NoError(t, err)

			// newConfig=nil → keep ciphertext; name/enabled/event_types replaced.
			require.NoError(t, repo.Update(ctx, id, "a2", true, []string{"grab.failed"}, nil))
			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, "a2", got.Name)
			assert.True(t, got.Enabled)
			assert.Equal(t, orig, got.ConfigEncrypted)
			assert.Equal(t, []string{"grab.failed"}, got.EventTypes)

			// newConfig non-nil → replace ciphertext.
			repl := []byte{0xCC}
			require.NoError(t, repo.Update(ctx, id, "a2", true, []string{"grab.failed"}, repl))
			got, err = repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, repl, got.ConfigEncrypted)
		})
	}
}

func TestAgentRepository_ListEnabledForEvent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAgentRepository(db)
			ctx := context.Background()

			// enabled + subscribed
			_, err := repo.Create(ctx, ports.NotificationAgent{Name: "yes", Enabled: true, ConfigEncrypted: []byte{1}, EventTypes: []string{"grab.failed", "import.failed"}})
			require.NoError(t, err)
			// disabled + subscribed
			_, err = repo.Create(ctx, ports.NotificationAgent{Name: "disabled", Enabled: false, ConfigEncrypted: []byte{2}, EventTypes: []string{"grab.failed"}})
			require.NoError(t, err)
			// enabled + not subscribed
			_, err = repo.Create(ctx, ports.NotificationAgent{Name: "other", Enabled: true, ConfigEncrypted: []byte{3}, EventTypes: []string{"grab.ok"}})
			require.NoError(t, err)

			got, err := repo.ListEnabledForEvent(ctx, "grab.failed")
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "yes", got[0].Name)
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
			ctx := context.Background()

			_, err := repo.Get(ctx, 999)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
			assert.True(t, errors.Is(repo.Delete(ctx, 999), ports.ErrNotFound))
			assert.True(t, errors.Is(repo.Update(ctx, 999, "x", true, nil, nil), ports.ErrNotFound))

			// Delete existing → ok.
			id, err := repo.Create(ctx, ports.NotificationAgent{Name: "d", ConfigEncrypted: []byte{9}, EventTypes: []string{"grab.ok"}})
			require.NoError(t, err)
			require.NoError(t, repo.Delete(ctx, id))
			_, err = repo.Get(ctx, id)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}
