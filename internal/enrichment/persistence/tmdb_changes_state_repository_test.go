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
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestTMDBChangesStateRepository_GetEmpty_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewTMDBChangesStateRepository(db)

			got, err := repo.Get(context.Background())
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound), "want ErrNotFound, got %v", err)
			assert.Equal(t, enrichmentpkg.ChangeCursor{}, got, "empty cursor on miss")
		})
	}
}

func TestTMDBChangesStateRepository_SaveGetRoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewTMDBChangesStateRepository(db)
			ctx := context.Background()

			windowEnd := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
			pollAt := time.Date(2026, 6, 25, 8, 30, 0, 0, time.UTC)
			in := enrichmentpkg.ChangeCursor{
				SchemaVersion: 1,
				LastWindowEnd: windowEnd,
				LastPollAt:    pollAt,
				LastMatched:   42,
				LastFirehose:  8500,
			}
			require.NoError(t, repo.Save(ctx, in))

			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, 1, got.SchemaVersion)
			assert.WithinDuration(t, windowEnd, got.LastWindowEnd, time.Second)
			assert.WithinDuration(t, pollAt, got.LastPollAt, time.Second)
			assert.Equal(t, 42, got.LastMatched)
			assert.Equal(t, 8500, got.LastFirehose)
		})
	}
}

// Zero LastWindowEnd / LastPollAt round-trip as SQL NULL → zero time (empty).
func TestTMDBChangesStateRepository_NullableRoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewTMDBChangesStateRepository(db)
			ctx := context.Background()

			// Save with a set window but a ZERO poll time.
			in := enrichmentpkg.ChangeCursor{
				LastWindowEnd: time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC),
				LastPollAt:    time.Time{}, // zero → NULL
			}
			require.NoError(t, repo.Save(ctx, in))

			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.False(t, got.LastWindowEnd.IsZero(), "window end persisted")
			assert.True(t, got.LastPollAt.IsZero(), "zero poll time round-trips as NULL")
			assert.Equal(t, 1, got.SchemaVersion, "zero schema_version defaults to 1")

			// Assert the underlying column is actually NULL.
			var m database.TMDBChangesStateModel
			require.NoError(t, db.Where("id = ?", int64(1)).First(&m).Error)
			assert.Nil(t, m.LastPollAt)
			assert.NotNil(t, m.LastWindowEnd)
		})
	}
}

// Save twice keeps a single row (id=1) and returns the latest values.
func TestTMDBChangesStateRepository_SaveTwice_SingleRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewTMDBChangesStateRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Save(ctx, enrichmentpkg.ChangeCursor{
				LastWindowEnd: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
				LastMatched:   1,
			}))
			require.NoError(t, repo.Save(ctx, enrichmentpkg.ChangeCursor{
				LastWindowEnd: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC),
				LastMatched:   9,
			}))

			var count int64
			require.NoError(t, db.Model(&database.TMDBChangesStateModel{}).Count(&count).Error)
			assert.EqualValues(t, 1, count, "single-row invariant")

			got, err := repo.Get(ctx)
			require.NoError(t, err)
			assert.WithinDuration(t, time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC),
				got.LastWindowEnd, time.Second)
			assert.Equal(t, 9, got.LastMatched, "latest values win")
		})
	}
}
