package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestSyncLogRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewSyncLogRepository(db)

			now := time.Now().UTC()
			entry := enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   42,
				Source:     enrichment.SourceTMDBSeries,
				SyncedAt:   &now,
				Outcome:    enrichment.OutcomeOK,
				ETag:       new("etag-001"),
				Attempts:   1,
			}
			require.NoError(t, repo.Upsert(ctx, entry))

			got, err := repo.GetLastSync(ctx, enrichment.EntityTypeSeries, 42, enrichment.SourceTMDBSeries)
			require.NoError(t, err)
			assert.Equal(t, enrichment.OutcomeOK, got.Outcome)
			assert.Equal(t, 1, got.Attempts)
			require.NotNil(t, got.ETag)
			assert.Equal(t, "etag-001", *got.ETag)
		})
	}
}

func TestSyncLogRepository_GetLastSync_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSyncLogRepository(db)
			_, err := repo.GetLastSync(context.Background(), enrichment.EntityTypeSeries, 1, enrichment.SourceTMDBSeries)
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestSyncLogRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewSyncLogRepository(db)

			now := time.Now().UTC()
			first := enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   42,
				Source:     enrichment.SourceTMDBSeries,
				SyncedAt:   &now,
				Outcome:    enrichment.OutcomeOK,
			}
			require.NoError(t, repo.Upsert(ctx, first))

			// Second call updates outcome to error + bumps attempts.
			retry := time.Now().UTC().Add(1 * time.Hour)
			second := enrichment.SyncLog{
				EntityType:    enrichment.EntityTypeSeries,
				EntityID:      42,
				Source:        enrichment.SourceTMDBSeries,
				Outcome:       enrichment.OutcomeError,
				ErrorDetail:   new("timeout"),
				Attempts:      2,
				NextAttemptAt: &retry,
			}
			require.NoError(t, repo.Upsert(ctx, second))

			got, err := repo.GetLastSync(ctx, enrichment.EntityTypeSeries, 42, enrichment.SourceTMDBSeries)
			require.NoError(t, err)
			assert.Equal(t, enrichment.OutcomeError, got.Outcome,
				"second upsert must overwrite outcome — single row by PK")
			assert.Equal(t, 2, got.Attempts)
			require.NotNil(t, got.ErrorDetail)
			assert.Equal(t, "timeout", *got.ErrorDetail)
		})
	}
}

func TestSyncLogRepository_Upsert_InvalidEnums(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewSyncLogRepository(db)

			require.Error(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityType("invalid"),
				EntityID:   1, Source: enrichment.SourceTMDBSeries,
			}))
			require.Error(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   1, Source: enrichment.Source("invalid"),
			}))
			require.Error(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries,
				EntityID:   0, Source: enrichment.SourceTMDBSeries,
			}))
		})
	}
}

// TestSyncLogRepository_StaleScan covers the nightly background sweep
// query (PRD §5.5): rows where outcome='ok' AND synced_at < cutoff,
// ordered by synced_at ASC, capped at limit.
func TestSyncLogRepository_StaleScan(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewSyncLogRepository(db)

			now := time.Now().UTC()
			old := now.Add(-72 * time.Hour)
			older := now.Add(-96 * time.Hour)
			fresh := now.Add(-1 * time.Hour)

			// Stale OK rows — MUST appear.
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 1,
				Source: enrichment.SourceTMDBSeries, SyncedAt: &old,
				Outcome: enrichment.OutcomeOK,
			}))
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 2,
				Source: enrichment.SourceTMDBSeries, SyncedAt: &older,
				Outcome: enrichment.OutcomeOK,
			}))

			// Fresh OK row — MUST NOT appear (younger than cutoff).
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 3,
				Source: enrichment.SourceTMDBSeries, SyncedAt: &fresh,
				Outcome: enrichment.OutcomeOK,
			}))

			// Error row — MUST NOT appear (outcome != ok).
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 4,
				Source: enrichment.SourceTMDBSeries, SyncedAt: &old,
				Outcome: enrichment.OutcomeError,
			}))

			// Wrong-source row — MUST NOT appear.
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypePerson, EntityID: 1,
				Source: enrichment.SourceTMDBPerson, SyncedAt: &old,
				Outcome: enrichment.OutcomeOK,
			}))

			cutoff := now.Add(-24 * time.Hour)
			rows, err := repo.StaleScan(ctx, enrichment.SourceTMDBSeries, cutoff, 10)
			require.NoError(t, err)
			require.Len(t, rows, 2,
				"StaleScan must return only outcome=ok rows older than cutoff for the requested source")
			// Ordered by synced_at ASC — oldest first.
			assert.Equal(t, int64(2), rows[0].EntityID,
				"older row must come first (synced_at ASC)")
			assert.Equal(t, int64(1), rows[1].EntityID)
		})
	}
}

func TestSyncLogRepository_StaleScan_RespectsLimit(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewSyncLogRepository(db)

			now := time.Now().UTC()
			old := now.Add(-72 * time.Hour)
			for i := int64(1); i <= 5; i++ {
				require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
					EntityType: enrichment.EntityTypeSeries, EntityID: i,
					Source: enrichment.SourceTMDBSeries, SyncedAt: &old,
					Outcome: enrichment.OutcomeOK,
				}))
			}

			rows, err := repo.StaleScan(ctx, enrichment.SourceTMDBSeries, now, 3)
			require.NoError(t, err)
			assert.Len(t, rows, 3, "StaleScan must respect the limit")
		})
	}
}

// TestSyncLogRepository_RetryDue covers the retry dispatcher query
// (PRD §5.5): rows where outcome='error' AND next_attempt_at <= now,
// ordered by next_attempt_at ASC.
func TestSyncLogRepository_RetryDue(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewSyncLogRepository(db)

			now := time.Now().UTC()
			overdueA := now.Add(-2 * time.Hour)
			overdueB := now.Add(-1 * time.Hour)
			future := now.Add(1 * time.Hour)

			// Two overdue errors — MUST appear, ordered by next_attempt_at ASC.
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 1,
				Source:  enrichment.SourceTMDBSeries,
				Outcome: enrichment.OutcomeError, NextAttemptAt: &overdueB,
			}))
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 2,
				Source:  enrichment.SourceTMDBSeries,
				Outcome: enrichment.OutcomeError, NextAttemptAt: &overdueA,
			}))

			// Future-retry error — MUST NOT appear (next_attempt_at > now).
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 3,
				Source:  enrichment.SourceTMDBSeries,
				Outcome: enrichment.OutcomeError, NextAttemptAt: &future,
			}))

			// OK row — MUST NOT appear (outcome != error).
			syncedNow := now
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypeSeries, EntityID: 4,
				Source: enrichment.SourceTMDBSeries, SyncedAt: &syncedNow,
				Outcome: enrichment.OutcomeOK,
			}))

			// Wrong-source error — MUST NOT appear.
			require.NoError(t, repo.Upsert(ctx, enrichment.SyncLog{
				EntityType: enrichment.EntityTypePerson, EntityID: 1,
				Source:  enrichment.SourceTMDBPerson,
				Outcome: enrichment.OutcomeError, NextAttemptAt: &overdueA,
			}))

			rows, err := repo.RetryDue(ctx, enrichment.SourceTMDBSeries, now, 10)
			require.NoError(t, err)
			require.Len(t, rows, 2,
				"RetryDue must return only error rows due for retry on the requested source")
			// Ordered by next_attempt_at ASC — most-overdue first.
			assert.Equal(t, int64(2), rows[0].EntityID,
				"most-overdue row must come first (next_attempt_at ASC)")
			assert.Equal(t, int64(1), rows[1].EntityID)
		})
	}
}
