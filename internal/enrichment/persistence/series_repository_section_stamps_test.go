package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesRepository_MarkTextSynced — single-column UPDATE stamps
// the row and bumps updated_at. Idempotent on re-call.
func TestSeriesRepository_MarkTextSynced(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, sampleCanon("Mark Text"))
			require.NoError(t, err)

			now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkTextSynced(ctx, id, now))

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.EnrichmentTextSyncedAt)
			assert.Equal(t, now.Unix(), got.EnrichmentTextSyncedAt.Unix())
		})
	}
}

// TestSeriesRepository_MarkCastSynced — analog for cast column.
func TestSeriesRepository_MarkCastSynced(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, sampleCanon("Mark Cast"))
			require.NoError(t, err)

			now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkCastSynced(ctx, id, now))

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.EnrichmentCastSyncedAt)
			assert.Equal(t, now.Unix(), got.EnrichmentCastSyncedAt.Unix())
		})
	}
}

// TestSeriesRepository_SectionStampsSurviveSonarrUpsert — I-1 ACCEPTANCE.
//
// Scenario: A2 narrow writer stamps enrichment_{text,cast,recs,media}_synced_at.
// Then Sonarr's 6h scan triggers Series.Upsert with a payload that
// has those four section columns nil (Sonarr never writes them — they
// are TMDB-domain). EXPECTED: COALESCE preserves the stamps.
//
// Pre-A2 fix this test fails with "stamp dropped to NULL".
//
// Per-column defensive coverage — A3a/A3b/A4 ship later, but the
// COALESCE diff covers all four в A2 to prevent reproducing the
// silent-drop class bug per-story.
func TestSeriesRepository_SectionStampsSurviveSonarrUpsert(t *testing.T) {
	t.Parallel()

	type stampCase struct {
		name     string
		stampFn  func(repo *SeriesRepository, ctx context.Context, id domain.SeriesID, now time.Time) error
		getField func(c series.Canon) *time.Time
	}

	cases := []stampCase{
		{
			name: "text",
			stampFn: func(r *SeriesRepository, ctx context.Context, id domain.SeriesID, now time.Time) error {
				return r.MarkTextSynced(ctx, id, now)
			},
			getField: func(c series.Canon) *time.Time { return c.EnrichmentTextSyncedAt },
		},
		{
			name: "cast",
			stampFn: func(r *SeriesRepository, ctx context.Context, id domain.SeriesID, now time.Time) error {
				return r.MarkCastSynced(ctx, id, now)
			},
			getField: func(c series.Canon) *time.Time { return c.EnrichmentCastSyncedAt },
		},
		// recs/media stamps are not exposed via SeriesRepo until A3b/A4.
		// We exercise the COALESCE path on those columns via direct SQL
		// (db.Exec) — the Repository layer covers it once A3b/A4 ship.
		{
			name: "recs",
			stampFn: func(r *SeriesRepository, ctx context.Context, id domain.SeriesID, now time.Time) error {
				return r.db.WithContext(ctx).Table("series").Where("id = ?", id).
					Updates(map[string]any{"enrichment_recs_synced_at": now.UTC()}).Error
			},
			getField: func(c series.Canon) *time.Time { return c.EnrichmentRecsSyncedAt },
		},
		{
			name: "media",
			stampFn: func(r *SeriesRepository, ctx context.Context, id domain.SeriesID, now time.Time) error {
				return r.db.WithContext(ctx).Table("series").Where("id = ?", id).
					Updates(map[string]any{"enrichment_media_synced_at": now.UTC()}).Error
			},
			getField: func(c series.Canon) *time.Time { return c.EnrichmentMediaSyncedAt },
		},
	}

	for _, backend := range testhelpers.AllBackends(t) {
		for _, tc := range cases {
			t.Run(backend.Name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				db := backend.NewDB(t)
				repo := NewSeriesRepository(db)
				ctx := context.Background()

				// 1. Seed canonical row.
				id, err := repo.Upsert(ctx, sampleCanon("I-1 acceptance "+tc.name))
				require.NoError(t, err)

				// 2. A2 writer stamps the section column.
				stampT := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
				require.NoError(t, tc.stampFn(repo, ctx, id, stampT))

				// Sanity: stamp landed.
				pre, err := repo.Get(ctx, id)
				require.NoError(t, err)
				require.NotNil(t, tc.getField(pre),
					"%s stamp must be set before the Sonarr-driven upsert", tc.name)

				// 3. Simulate Sonarr-driven scan: re-Upsert with the
				//    SAME canonical row (sampleCanon does NOT carry
				//    any enrichment_*_synced_at field — Sonarr never
				//    writes them).
				_, err = repo.Upsert(ctx, sampleCanon("I-1 acceptance "+tc.name))
				require.NoError(t, err)

				// 4. Assert stamp PRESERVED via COALESCE.
				got, err := repo.Get(ctx, id)
				require.NoError(t, err)
				require.NotNil(t, tc.getField(got),
					"%s stamp must survive Sonarr-driven Upsert (COALESCE)", tc.name)
				assert.Equal(t, stampT.Unix(), tc.getField(got).Unix(),
					"%s stamp value must be unchanged", tc.name)
			})
		}
	}
}
