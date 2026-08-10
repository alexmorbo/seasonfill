package app

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

const testMasterKey = "agents-usecase-test-master-key"

func newUC(t *testing.T, repo ports.NotificationAgentRepository, n Notifier) (*AgentsUseCase, *crypto.Cipher) {
	t.Helper()
	c, err := crypto.NewNotificationAgentCipher(testMasterKey)
	require.NoError(t, err)
	return NewAgentsUseCase(repo, c, n), c
}

func TestAgentsUseCase_Create_SealsURL(t *testing.T) {
	t.Parallel()
	const url = "telegram://SECRET-TOKEN@telegram?chats=1"
	var stored ports.NotificationAgent
	repo := &ports.NotificationAgentRepositoryMock{
		CreateFunc: func(_ context.Context, a ports.NotificationAgent) (int64, error) { stored = a; return 1, nil },
	}
	uc, cipher := newUC(t, repo, &stubNotifier{})

	id, err := uc.Create(context.Background(), "tg", url, true, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 1, id)
	require.NotEmpty(t, stored.ConfigEncrypted)
	assert.False(t, bytes.Contains(stored.ConfigEncrypted, []byte("SECRET-TOKEN")), "ciphertext must not contain plaintext token")
	// default event types applied (grab.ok absent).
	assert.Equal(t, DefaultEventTypes, stored.EventTypes)
	assert.NotContains(t, stored.EventTypes, "grab.ok")
	// round-trips.
	raw, err := cipher.Open(stored.ConfigEncrypted)
	require.NoError(t, err)
	assert.Equal(t, url, string(raw))
}

func TestAgentsUseCase_Create_Validation(t *testing.T) {
	t.Parallel()
	repo := &ports.NotificationAgentRepositoryMock{
		CreateFunc: func(context.Context, ports.NotificationAgent) (int64, error) { return 1, nil },
	}
	uc, _ := newUC(t, repo, &stubNotifier{})
	ctx := context.Background()

	_, err := uc.Create(ctx, "", "telegram://t@x", true, nil)
	assert.ErrorIs(t, err, ErrInvalidAgent) // empty name
	_, err = uc.Create(ctx, "n", "", true, nil)
	assert.ErrorIs(t, err, ErrInvalidAgent) // empty url
	_, err = uc.Create(ctx, "n", "telegram://t@x", true, []string{"bogus.event"})
	assert.ErrorIs(t, err, ErrInvalidAgent) // unknown event_type
}

func TestAgentsUseCase_Create_AcceptsCalendarEventTypes(t *testing.T) {
	t.Parallel()
	var stored ports.NotificationAgent
	repo := &ports.NotificationAgentRepositoryMock{
		CreateFunc: func(_ context.Context, a ports.NotificationAgent) (int64, error) { stored = a; return 1, nil },
	}
	uc, _ := newUC(t, repo, &stubNotifier{})
	ctx := context.Background()

	// The N3 calendar/digest event types must be subscribable (regression: the
	// producers emitted these but the allow-list rejected them with 400).
	for _, et := range []string{"season.premiere", "air_date.announced", "digest.weekly"} {
		_, err := uc.Create(ctx, "n", "telegram://t@x", true, []string{et})
		require.NoErrorf(t, err, "event_type %q must be accepted", et)
		assert.Equal(t, []string{et}, stored.EventTypes)
	}

	// Defaults (nil input) subscribe to every known type except grab.ok.
	_, err := uc.Create(ctx, "n", "telegram://t@x", true, nil)
	require.NoError(t, err)
	assert.Contains(t, stored.EventTypes, "season.premiere")
	assert.Contains(t, stored.EventTypes, "air_date.announced")
	assert.Contains(t, stored.EventTypes, "digest.weekly")
	assert.NotContains(t, stored.EventTypes, "grab.ok")
}

func TestAgentsUseCase_Update_URLSemantics(t *testing.T) {
	t.Parallel()
	var lastNewConfig []byte
	var configSet bool
	repo := &ports.NotificationAgentRepositoryMock{
		UpdateFunc: func(_ context.Context, _ int64, _ string, _ bool, _ []string, newConfig []byte) error {
			lastNewConfig = newConfig
			configSet = true
			return nil
		},
	}
	uc, _ := newUC(t, repo, &stubNotifier{})
	ctx := context.Background()

	// empty url → newConfig nil (keep).
	require.NoError(t, uc.Update(ctx, 1, "n", "", true, []string{"grab.failed"}))
	require.True(t, configSet)
	assert.Nil(t, lastNewConfig)

	// non-empty url → newConfig non-nil (replace).
	require.NoError(t, uc.Update(ctx, 1, "n", "discord://x@y", true, []string{"grab.failed"}))
	assert.NotNil(t, lastNewConfig)
}

func TestAgentsUseCase_ListView_Masked(t *testing.T) {
	t.Parallel()
	c, err := crypto.NewNotificationAgentCipher(testMasterKey)
	require.NoError(t, err)
	enc, err := c.Seal([]byte("telegram://TOKEN@telegram?chats=9"))
	require.NoError(t, err)
	repo := &ports.NotificationAgentRepositoryMock{
		ListFunc: func(context.Context) ([]ports.NotificationAgent, error) {
			return []ports.NotificationAgent{{ID: 1, Name: "tg", Enabled: true, ConfigEncrypted: enc, EventTypes: []string{"grab.failed"}}}, nil
		},
	}
	uc := NewAgentsUseCase(repo, c, &stubNotifier{})

	views, err := uc.List(context.Background())
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.True(t, views[0].Configured)
	assert.Equal(t, "telegram", views[0].Scheme)
}

func TestAgentsUseCase_Test_CallsNotifier(t *testing.T) {
	t.Parallel()
	c, err := crypto.NewNotificationAgentCipher(testMasterKey)
	require.NoError(t, err)
	enc, err := c.Seal([]byte("telegram://t@x"))
	require.NoError(t, err)
	repo := &ports.NotificationAgentRepositoryMock{
		GetFunc: func(context.Context, int64) (ports.NotificationAgent, error) {
			return ports.NotificationAgent{ID: 1, ConfigEncrypted: enc}, nil
		},
	}
	n := &stubNotifier{}
	uc := NewAgentsUseCase(repo, c, n)

	require.NoError(t, uc.Test(context.Background(), 1))
	assert.Equal(t, 1, n.calls)

	// Not found propagates.
	repo.GetFunc = func(context.Context, int64) (ports.NotificationAgent, error) {
		return ports.NotificationAgent{}, ports.ErrNotFound
	}
	assert.True(t, errors.Is(uc.Test(context.Background(), 2), ports.ErrNotFound))
}
