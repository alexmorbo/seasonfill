package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestI18nCoverageRepository_BaseLangCoverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			gdb := backend.NewDB(t)

			seriesRepo := NewSeriesRepository(gdb)
			textsRepo := NewSeriesTextsRepository(gdb)

			// Series 1: TMDB, WITH en-US series_texts (covered).
			c1 := sampleCanon("Covered TMDB")
			c1.TMDBID = ptrTMDBID(1001)
			sid1, err := seriesRepo.Upsert(ctx, c1)
			require.NoError(t, err)
			title1 := "en title"
			require.NoError(t, textsRepo.Upsert(ctx, series.SeriesText{
				SeriesID: sid1, Language: "en-US", Title: &title1,
			}))

			// Series 2: TMDB, WITHOUT en-US series_texts (uncovered).
			c2 := sampleCanon("Uncovered TMDB")
			c2.TMDBID = ptrTMDBID(1002)
			_, err = seriesRepo.Upsert(ctx, c2)
			require.NoError(t, err)

			// Series 3: tmdb-less — must NOT count in the denominator at all.
			c3 := sampleCanon("Sonarr Only")
			c3.TMDBID = nil
			c3.TVDBID = ptrTVDBID(9999)
			c3.IMDBID = nil
			sid3, err := seriesRepo.Upsert(ctx, c3)
			require.NoError(t, err)
			// Even though it has an en-US row, it is out of scope (no tmdb_id).
			t3 := "sonarr latin"
			require.NoError(t, textsRepo.InsertBaseLangIfAbsent(ctx, series.SeriesText{
				SeriesID: sid3, Language: "en-US", Title: &t3,
			}))

			rows, err := NewI18nCoverageRepository(gdb).BaseLangCoverage(ctx)
			require.NoError(t, err)

			byTable := map[string]BaseCoverageRow{}
			for _, r := range rows {
				byTable[r.Table] = r
			}
			require.Len(t, byTable, 5)

			st := byTable["series_texts"]
			assert.Equal(t, int64(2), st.Total, "only the 2 tmdb series count")
			assert.Equal(t, int64(1), st.Covered, "series 3 (tmdb-less) excluded despite en-US row")

			smt := byTable["series_media_texts"]
			assert.Equal(t, int64(2), smt.Total)
			assert.Equal(t, int64(0), smt.Covered, "no media rows seeded")

			// Season / episode tables have no rows seeded → covered 0, total 0.
			for _, tbl := range []string{"season_texts", "season_media_texts", "episode_texts"} {
				assert.Equal(t, int64(0), byTable[tbl].Total, tbl)
				assert.Equal(t, int64(0), byTable[tbl].Covered, tbl)
			}
		})
	}
}
