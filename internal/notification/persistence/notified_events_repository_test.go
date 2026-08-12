package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestNotifiedEventsRepository_MarkIfNew(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedAgentUser(t, db)
			seedSecondUser(t, db)
			repo := NewNotifiedEventsRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			created, err := repo.MarkIfNew(ctx, agentOwnerID, "season.premiere", "42:2", now)
			require.NoError(t, err)
			assert.True(t, created, "first insert must create a marker")

			created, err = repo.MarkIfNew(ctx, agentOwnerID, "season.premiere", "42:2", now)
			require.NoError(t, err)
			assert.False(t, created, "second insert of the same key must be a no-op")

			// Same (event_type, entity_key) for a DIFFERENT user is independent
			// (per-user dedup, Ф8-U-5c).
			created, err = repo.MarkIfNew(ctx, secondUserID, "season.premiere", "42:2", now)
			require.NoError(t, err)
			assert.True(t, created, "a different user must get its own marker")

			// Different key on same event_type is independent.
			created, err = repo.MarkIfNew(ctx, agentOwnerID, "season.premiere", "42:3", now)
			require.NoError(t, err)
			assert.True(t, created)

			// Same key on a different event_type is independent.
			created, err = repo.MarkIfNew(ctx, agentOwnerID, "air_date.announced", "42:2", now)
			require.NoError(t, err)
			assert.True(t, created)
		})
	}
}

func TestNotifiedEventsRepository_MarkIfNew_RejectsEmpty(t *testing.T) {
	t.Parallel()
	db := testhelpers.AllBackends(t)[0].NewDB(t)
	repo := NewNotifiedEventsRepository(db)
	_, err := repo.MarkIfNew(context.Background(), agentOwnerID, "", "k", time.Now())
	require.Error(t, err)
	_, err = repo.MarkIfNew(context.Background(), agentOwnerID, "e", "", time.Now())
	require.Error(t, err)
}
