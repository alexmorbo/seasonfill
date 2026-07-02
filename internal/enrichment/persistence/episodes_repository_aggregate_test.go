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

// TestEpisodesRepository_AggregateBySeries_D0 is the D-0 dual-backend suite for
// the E-1 B3c AggregateBySeries GROUP BY read. It runs against BOTH SQLite
// (always) and testcontainers Postgres (SEASONFILL_TEST_POSTGRES_ENABLE=1) so the
// MIN/MAX(air_date) ordering — the one new SQL surface B3c introduces — is proven
// cross-dialect (Postgres native timestamp ordering vs SQLite ISO-8601 text).
func TestEpisodesRepository_AggregateBySeries_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (domain.SeriesID, *EpisodesRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Aggregate Show"))
				require.NoError(t, err)
				return seriesID, NewEpisodesRepository(gdb)
			}

			mkDate := func(y int, m time.Month, d int) *time.Time {
				return new(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
			}

			t.Run("happy_multi_season_min_max", func(t *testing.T) {
				sid, repo := seed(t)
				_, err := repo.BatchUpsert(ctx, []series.CanonEpisode{
					{SeriesID: sid, SeasonNumber: 1, EpisodeNumber: 1, AirDate: mkDate(2024, 1, 1)},
					{SeriesID: sid, SeasonNumber: 1, EpisodeNumber: 2, AirDate: mkDate(2024, 2, 1)},
					{SeriesID: sid, SeasonNumber: 2, EpisodeNumber: 1, AirDate: mkDate(2025, 3, 1)},
					{SeriesID: sid, SeasonNumber: 2, EpisodeNumber: 2, AirDate: mkDate(2025, 6, 1)},
					{SeriesID: sid, SeasonNumber: 2, EpisodeNumber: 3, AirDate: mkDate(2025, 5, 1)},
				})
				require.NoError(t, err)

				out, err := repo.AggregateBySeries(ctx, sid)
				require.NoError(t, err)
				require.Len(t, out, 2)

				assert.Equal(t, 2, out[1].EpisodeCount)
				require.NotNil(t, out[1].FirstAirDate)
				require.NotNil(t, out[1].LastAirDate)
				assert.True(t, out[1].FirstAirDate.Equal(*mkDate(2024, 1, 1)), "S1 MIN")
				assert.True(t, out[1].LastAirDate.Equal(*mkDate(2024, 2, 1)), "S1 MAX")

				assert.Equal(t, 3, out[2].EpisodeCount)
				require.NotNil(t, out[2].FirstAirDate)
				require.NotNil(t, out[2].LastAirDate)
				assert.True(t, out[2].FirstAirDate.Equal(*mkDate(2025, 3, 1)), "S2 MIN")
				assert.True(t, out[2].LastAirDate.Equal(*mkDate(2025, 6, 1)), "S2 MAX")
			})

			// air_date_end = MAX aggregate: out-of-order insert must still yield
			// the latest date as LastAirDate and earliest as FirstAirDate.
			t.Run("air_date_end_is_max_out_of_order", func(t *testing.T) {
				sid, repo := seed(t)
				_, err := repo.BatchUpsert(ctx, []series.CanonEpisode{
					{SeriesID: sid, SeasonNumber: 1, EpisodeNumber: 1, AirDate: mkDate(2024, 1, 1)},
					{SeriesID: sid, SeasonNumber: 1, EpisodeNumber: 2, AirDate: mkDate(2024, 3, 1)},
					{SeriesID: sid, SeasonNumber: 1, EpisodeNumber: 3, AirDate: mkDate(2024, 2, 1)},
				})
				require.NoError(t, err)

				out, err := repo.AggregateBySeries(ctx, sid)
				require.NoError(t, err)
				require.NotNil(t, out[1].LastAirDate)
				require.NotNil(t, out[1].FirstAirDate)
				assert.True(t, out[1].LastAirDate.Equal(*mkDate(2024, 3, 1)))
				assert.True(t, out[1].FirstAirDate.Equal(*mkDate(2024, 1, 1)))
			})

			// NULL air_date pair: every episode has NULL air_date → nil
			// First/LastAirDate but EpisodeCount still counts the rows.
			t.Run("all_null_air_date_counts_rows_nil_dates", func(t *testing.T) {
				sid, repo := seed(t)
				_, err := repo.BatchUpsert(ctx, []series.CanonEpisode{
					{SeriesID: sid, SeasonNumber: 0, EpisodeNumber: 1, AirDate: nil},
					{SeriesID: sid, SeasonNumber: 0, EpisodeNumber: 2, AirDate: nil},
				})
				require.NoError(t, err)

				out, err := repo.AggregateBySeries(ctx, sid)
				require.NoError(t, err)
				require.Contains(t, out, 0)
				assert.Equal(t, 2, out[0].EpisodeCount)
				assert.Nil(t, out[0].FirstAirDate)
				assert.Nil(t, out[0].LastAirDate)
			})

			t.Run("empty_series_returns_empty_map", func(t *testing.T) {
				sid, repo := seed(t)
				out, err := repo.AggregateBySeries(ctx, sid)
				require.NoError(t, err)
				assert.Empty(t, out)
			})

			// error pair: cancelled context → non-nil error, nil map.
			t.Run("cancelled_context_error_pair", func(t *testing.T) {
				sid, repo := seed(t)
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				out, err := repo.AggregateBySeries(cctx, sid)
				require.Error(t, err)
				assert.Nil(t, out)
			})
		})
	}
}
