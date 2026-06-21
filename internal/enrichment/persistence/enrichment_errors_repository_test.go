package persistence

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// randEntityID returns a randomised int64 in the positive range. Used
// to scope parallel sub-tests so two t.Parallel() goroutines never
// collide on the (entity_type, entity_id, source) composite UNIQUE.
// The D-0 quality bar requires uuid-style isolation for any parallel
// fixture; int64 random is the closest analog without pulling
// uuid into the test path.
func randEntityID(t *testing.T) int64 {
	t.Helper()
	v := rand.Int63()
	if v <= 0 {
		v = 1
	}
	return v
}

func TestEnrichmentErrorsRepository_RecordFailure_NewRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			entityID := randEntityID(t)
			err := repo.RecordFailure(ctx, enrichment.EnrichmentError{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   entityID,
				Source:     enrichment.SourceTMDBSeries,
				LastError:  "timeout: ctx deadline exceeded",
			})
			require.NoError(t, err)

			got, err := repo.GetByEntitySource(ctx, enrichment.EntityTypeSeries, entityID, enrichment.SourceTMDBSeries)
			require.NoError(t, err)
			assert.Equal(t, entityID, got.EntityID)
			assert.Equal(t, enrichment.EntityTypeSeries, got.EntityType)
			assert.Equal(t, enrichment.SourceTMDBSeries, got.Source)
			assert.Equal(t, "timeout: ctx deadline exceeded", got.LastError)
			assert.Equal(t, 1, got.Attempts, "default attempts=1 when zero")
			assert.False(t, got.FirstSeenAt.IsZero(), "first_seen_at populated")
			assert.False(t, got.LastSeenAt.IsZero(), "last_seen_at populated")
			assert.Nil(t, got.NextAttemptAt, "next_attempt_at nil when not scheduled")
		})
	}
}

func TestEnrichmentErrorsRepository_RecordFailure_UpdateExisting(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			entityID := randEntityID(t)
			err := repo.RecordFailure(ctx, enrichment.EnrichmentError{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   entityID,
				Source:     enrichment.SourceOMDb,
				LastError:  "first failure",
			})
			require.NoError(t, err)

			first, err := repo.GetByEntitySource(ctx, enrichment.EntityTypeSeries, entityID, enrichment.SourceOMDb)
			require.NoError(t, err)
			firstSeen := first.FirstSeenAt

			// Sleep one millisecond so last_seen_at definitely differs from
			// first_seen_at on platforms with coarse time resolution.
			time.Sleep(1 * time.Millisecond)

			nextRetry := time.Now().UTC().Add(15 * time.Minute)
			err = repo.RecordFailure(ctx, enrichment.EnrichmentError{
				EntityType:    enrichment.EntityTypeSeries,
				EntityID:      entityID,
				Source:        enrichment.SourceOMDb,
				LastError:     "second failure",
				Attempts:      2,
				NextAttemptAt: &nextRetry,
			})
			require.NoError(t, err)

			second, err := repo.GetByEntitySource(ctx, enrichment.EntityTypeSeries, entityID, enrichment.SourceOMDb)
			require.NoError(t, err)
			assert.Equal(t, "second failure", second.LastError, "last_error bumped")
			assert.Equal(t, 2, second.Attempts, "attempts bumped")
			require.NotNil(t, second.NextAttemptAt)
			assert.WithinDuration(t, nextRetry, *second.NextAttemptAt, time.Second)
			assert.True(t, second.LastSeenAt.After(firstSeen) || second.LastSeenAt.Equal(firstSeen),
				"last_seen_at bumped or equal")
			assert.WithinDuration(t, firstSeen, second.FirstSeenAt, time.Second,
				"first_seen_at PRESERVED across UPSERT")
		})
	}
}

func TestEnrichmentErrorsRepository_RecordFailure_ValidatesEntityType(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			cases := []struct {
				name string
				in   enrichment.EnrichmentError
				want string
			}{
				{
					name: "bad entity_type",
					in: enrichment.EnrichmentError{
						EntityType: enrichment.EntityType("garbage"),
						EntityID:   1,
						Source:     enrichment.SourceTMDBSeries,
						LastError:  "x",
					},
					want: "invalid entity_type",
				},
				{
					name: "zero entity_id",
					in: enrichment.EnrichmentError{
						EntityType: enrichment.EntityTypeSeries,
						EntityID:   0,
						Source:     enrichment.SourceTMDBSeries,
						LastError:  "x",
					},
					want: "entity_id must be non-zero",
				},
				{
					name: "bad source",
					in: enrichment.EnrichmentError{
						EntityType: enrichment.EntityTypeSeries,
						EntityID:   1,
						Source:     enrichment.Source("garbage"),
						LastError:  "x",
					},
					want: "invalid source",
				},
				{
					name: "empty last_error",
					in: enrichment.EnrichmentError{
						EntityType: enrichment.EntityTypeSeries,
						EntityID:   1,
						Source:     enrichment.SourceTMDBSeries,
						LastError:  "",
					},
					want: "last_error must be non-empty",
				},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					err := repo.RecordFailure(ctx, tc.in)
					require.Error(t, err)
					assert.Contains(t, err.Error(), tc.want)
				})
			}
		})
	}
}

func TestEnrichmentErrorsRepository_ClearOnSuccess_RemovesRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			entityID := randEntityID(t)
			require.NoError(t, repo.RecordFailure(ctx, enrichment.EnrichmentError{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   entityID,
				Source:     enrichment.SourceTMDBSeries,
				LastError:  "boom",
			}))

			require.NoError(t, repo.ClearOnSuccess(ctx,
				enrichment.EntityTypeSeries, entityID, enrichment.SourceTMDBSeries))

			_, err := repo.GetByEntitySource(ctx,
				enrichment.EntityTypeSeries, entityID, enrichment.SourceTMDBSeries)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound),
				"cleared row returns ports.ErrNotFound")
		})
	}
}

func TestEnrichmentErrorsRepository_ClearOnSuccess_NoOp(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			// Never-recorded entity. Idempotent — must not error.
			err := repo.ClearOnSuccess(ctx,
				enrichment.EntityTypeSeries, randEntityID(t), enrichment.SourceTMDBSeries)
			assert.NoError(t, err)
		})
	}
}

func TestEnrichmentErrorsRepository_GetForEntity_ReturnsAllSources(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			entityID := randEntityID(t)
			for _, src := range []enrichment.Source{
				enrichment.SourceTMDBSeries,
				enrichment.SourceTMDBSeason,
				enrichment.SourceOMDb,
			} {
				require.NoError(t, repo.RecordFailure(ctx, enrichment.EnrichmentError{
					EntityType: enrichment.EntityTypeSeries,
					EntityID:   entityID,
					Source:     src,
					LastError:  "boom: " + string(src),
				}))
			}

			rows, err := repo.GetForEntity(ctx, enrichment.EntityTypeSeries, entityID)
			require.NoError(t, err)
			require.Len(t, rows, 3)
			// Ordered by source ASC: omdb < tmdb_season < tmdb_series
			assert.Equal(t, enrichment.SourceOMDb, rows[0].Source)
			assert.Equal(t, enrichment.SourceTMDBSeason, rows[1].Source)
			assert.Equal(t, enrichment.SourceTMDBSeries, rows[2].Source)
		})
	}
}

func TestEnrichmentErrorsRepository_GetForEntity_EmptyWhenNone(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			rows, err := repo.GetForEntity(ctx, enrichment.EntityTypeSeries, randEntityID(t))
			require.NoError(t, err, "no rows is NOT an error path")
			assert.Empty(t, rows)
			assert.NotNil(t, rows, "empty slice (not nil) keeps callers iterating safely")
		})
	}
}

func TestEnrichmentErrorsRepository_ListDueForRetry(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			now := time.Now().UTC()
			past := now.Add(-1 * time.Hour)
			future := now.Add(1 * time.Hour)

			entityA := randEntityID(t)
			entityB := randEntityID(t)
			entityC := randEntityID(t)

			require.NoError(t, repo.RecordFailure(ctx, enrichment.EnrichmentError{
				EntityType:    enrichment.EntityTypeSeries,
				EntityID:      entityA,
				Source:        enrichment.SourceTMDBSeries,
				LastError:     "due",
				NextAttemptAt: &past,
			}))
			require.NoError(t, repo.RecordFailure(ctx, enrichment.EnrichmentError{
				EntityType:    enrichment.EntityTypeSeries,
				EntityID:      entityB,
				Source:        enrichment.SourceTMDBSeries,
				LastError:     "not due yet",
				NextAttemptAt: &future,
			}))
			require.NoError(t, repo.RecordFailure(ctx, enrichment.EnrichmentError{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   entityC,
				Source:     enrichment.SourceTMDBSeries,
				LastError:  "unscheduled (NULL next_attempt_at)",
				// NextAttemptAt left nil — must NOT appear in retry queue.
			}))

			rows, err := repo.ListDueForRetry(ctx, enrichment.SourceTMDBSeries, now, 100)
			require.NoError(t, err)

			ids := make([]int64, 0, len(rows))
			for _, r := range rows {
				ids = append(ids, r.EntityID)
			}
			assert.Contains(t, ids, entityA, "past next_attempt_at is due")
			assert.NotContains(t, ids, entityB, "future next_attempt_at is NOT due")
			assert.NotContains(t, ids, entityC, "NULL next_attempt_at is NOT due")
		})
	}
}
