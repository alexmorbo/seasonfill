package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	enrichmentpkg "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestMovieChangesStateRepository_GetEmpty_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieChangesStateRepository(backend.NewDB(t))

			got, err := repo.Get(context.Background())
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound), "want ErrNotFound, got %v", err)
			assert.Equal(t, enrichmentpkg.ChangeCursor{}, got, "empty cursor on miss")
		})
	}
}

func TestMovieChangesStateRepository_SaveGetRoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieChangesStateRepository(backend.NewDB(t))
			ctx := context.Background()

			windowEnd := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
			pollAt := time.Date(2026, 6, 25, 8, 30, 0, 0, time.UTC)
			in := enrichmentpkg.ChangeCursor{
				SchemaVersion: 1,
				LastWindowEnd: windowEnd,
				LastPollAt:    pollAt,
				LastMatched:   7,
				LastFirehose:  1200,
			}
			require.NoError(t, repo.Save(ctx, in))

			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, 1, got.SchemaVersion)
			assert.WithinDuration(t, windowEnd, got.LastWindowEnd, time.Second)
			assert.WithinDuration(t, pollAt, got.LastPollAt, time.Second)
			assert.Equal(t, 7, got.LastMatched)
			assert.Equal(t, 1200, got.LastFirehose)

			// Second Save updates the SAME single row (id=1) — no duplicate.
			in.LastMatched = 9
			require.NoError(t, repo.Save(ctx, in))
			got2, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, 9, got2.LastMatched)
		})
	}
}
