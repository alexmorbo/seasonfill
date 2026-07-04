package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesTextsRepository_FallbackThreeScenarios covers the §5.6
// pattern: requested-language hit, en-US fallback, first-available
// when neither requested nor en-US exists.
func TestSeriesTextsRepository_FallbackThreeScenarios(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			cases := []struct {
				name      string
				seed      []series.SeriesText
				requested string
				wantLang  string
				wantTitle string
			}{
				{
					name: "requested language present",
					seed: []series.SeriesText{
						{Language: "ru-RU", Title: new("Фонд")},
						{Language: "en-US", Title: new("Foundation")},
					},
					requested: "ru-RU",
					wantLang:  "ru-RU",
					wantTitle: "Фонд",
				},
				{
					name: "requested missing, en-US fallback",
					seed: []series.SeriesText{
						{Language: "en-US", Title: new("Foundation")},
					},
					requested: "ru-RU",
					wantLang:  "en-US",
					wantTitle: "Foundation",
				},
				{
					name: "requested and en-US missing, first available wins",
					seed: []series.SeriesText{
						{Language: "fr-FR", Title: new("Fondation")},
						{Language: "de-DE", Title: new("Stiftung")},
					},
					requested: "ru-RU",
					wantLang:  "de-DE", // language ASC tiebreaker — 'd' < 'f'
					wantTitle: "Stiftung",
				},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					// Fresh DB per scenario keeps the test deterministic without
					// having to scrub rows between cases.
					db := backend.NewDB(t)
					seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
					require.NoError(t, err)
					repo := NewSeriesTextsRepository(db)
					for _, row := range tc.seed {
						row.SeriesID = seriesID
						require.NoError(t, repo.Upsert(ctx, row))
					}
					got, err := repo.GetWithFallback(ctx, seriesID, tc.requested)
					require.NoError(t, err)
					assert.Equal(t, tc.wantLang, got.Language)
					require.NotNil(t, got.Title)
					assert.Equal(t, tc.wantTitle, *got.Title)
				})
			}
		})
	}
}

// TestSeriesTextsRepository_Fallback_NoRows confirms the helper returns
// ErrNotFound when the entity has no text rows at all.
func TestSeriesTextsRepository_Fallback_NoRows(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
			require.NoError(t, err)
			repo := NewSeriesTextsRepository(db)
			_, err = repo.GetWithFallback(ctx, seriesID, "ru-RU")
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

// Story 566 — RecommendationsCoverage validates the parent-scoped
// coverage counter used by Probe SectionRecommendations. Dual-backend
// (testcontainers Postgres + SQLite) via testhelpers.AllBackends.
func TestSeriesTextsRepository_RecommendationsCoverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			cases := []struct {
				name        string
				recCount    int // 0 == no recommendations
				coverRuRU   int // subset of recs with a ru-RU series_texts row
				coverEnUS   int // additional recs with only en-US row (to prove filter)
				queryLang   string
				wantCovered int
				wantTotal   int
			}{
				{
					name:        "no recommendations at all",
					recCount:    0,
					queryLang:   "ru-RU",
					wantCovered: 0,
					wantTotal:   0,
				},
				{
					name:        "all recs have requested lang",
					recCount:    3,
					coverRuRU:   3,
					queryLang:   "ru-RU",
					wantCovered: 3,
					wantTotal:   3,
				},
				{
					name:        "partial coverage — 4 of 20 (series 691 live)",
					recCount:    20,
					coverRuRU:   4,
					queryLang:   "ru-RU",
					wantCovered: 4,
					wantTotal:   20,
				},
				{
					name:        "zero coverage — 20 recs, 0 ru-RU (series 701 live)",
					recCount:    20,
					coverRuRU:   0,
					queryLang:   "ru-RU",
					wantCovered: 0,
					wantTotal:   20,
				},
				{
					name:        "different lang requested — en-US present, ru-RU counted",
					recCount:    3,
					coverRuRU:   1,
					coverEnUS:   2, // rec[1], rec[2] have en-US only — must NOT count for ru-RU
					queryLang:   "ru-RU",
					wantCovered: 1,
					wantTotal:   3,
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					db := backend.NewDB(t)
					seriesRepo := NewSeriesRepository(db)
					recsRepo := NewRecommendationsRepository(db)
					textsRepo := NewSeriesTextsRepository(db)

					// Seed parent series (assigned auto id).
					parentID, err := seriesRepo.Upsert(ctx, sampleCanon("Parent "+tc.name))
					require.NoError(t, err)

					// Seed rec target series (assigned auto ids); tmdb_id unique
					// per-row to avoid partial-unique conflict across test cases.
					recIDs := make([]domain.SeriesID, 0, tc.recCount)
					for i := 0; i < tc.recCount; i++ {
						c := sampleCanon(fmt.Sprintf("Rec %s %d", tc.name, i))
						c.TMDBID = ptrTMDBID(500000 + int(parentID)*100 + i)
						rid, err := seriesRepo.Upsert(ctx, c)
						require.NoError(t, err)
						recIDs = append(recIDs, rid)
					}

					if tc.recCount > 0 {
						require.NoError(t, recsRepo.Set(ctx, parentID, recIDs))
					}
					for i := 0; i < tc.coverRuRU; i++ {
						require.NoError(t, textsRepo.Upsert(ctx, series.SeriesText{
							SeriesID: recIDs[i],
							Language: "ru-RU",
							Title:    new("ru title"),
						}))
					}
					// en-US rows go on the recs AFTER the ru-RU-covered subset.
					for i := tc.coverRuRU; i < tc.coverRuRU+tc.coverEnUS && i < len(recIDs); i++ {
						require.NoError(t, textsRepo.Upsert(ctx, series.SeriesText{
							SeriesID: recIDs[i],
							Language: "en-US",
							Title:    new("en title"),
						}))
					}

					covered, total, err := textsRepo.RecommendationsCoverage(ctx, parentID, tc.queryLang)
					require.NoError(t, err)
					assert.Equal(t, tc.wantCovered, covered, "covered")
					assert.Equal(t, tc.wantTotal, total, "total")
				})
			}
		})
	}
}

// TestSeriesTextsRepository_RecommendationsCoverage_QueryError — table
// pair with NULL/error case: repository with a closed DB pool returns
// error, matching Radarr fail-open expectation в probe wiring.
func TestSeriesTextsRepository_RecommendationsCoverage_QueryError(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesTextsRepository(db)

			// Close the underlying sql.DB to force IO error on the next query.
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())

			_, _, err = repo.RecommendationsCoverage(context.Background(), 1, "ru-RU")
			require.Error(t, err)
		})
	}
}

// TestEpisodeTextsRepository_FallbackSmoke is a smaller smoke that
// confirms the same helper wired to episode_texts gives en-US fallback.
func TestEpisodeTextsRepository_FallbackSmoke(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
			require.NoError(t, err)
			epIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
				SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
			})
			require.NoError(t, err)
			epID := domain.EpisodeID(epIDRaw)
			repo := NewEpisodeTextsRepository(db)
			require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
				EpisodeID: epID, Language: "en-US", Title: new("Good News About Hell"),
			}))
			got, err := repo.GetWithFallback(ctx, epID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "en-US", got.Language)
		})
	}
}

// TestEpisodeTextsRepository_CoverageBySeriesSeason proves W16-7: the
// per-season coverage query scopes numerator AND denominator to one
// season, so a fully localized season 1 reads 100% even while season 2
// is empty — whereas the old series-wide CoverageBySeries reads 50% and
// would keep re-flagging season 1 stale at the 80% threshold.
func TestEpisodeTextsRepository_CoverageBySeriesSeason(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
			require.NoError(t, err)

			epRepo := NewEpisodesRepository(db)
			txtRepo := NewEpisodeTextsRepository(db)

			// Season 1: 3 episodes, all with a ru-RU episode_texts row.
			for ep := 1; ep <= 3; ep++ {
				epIDRaw, uErr := epRepo.Upsert(ctx, series.CanonEpisode{
					SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: ep,
				})
				require.NoError(t, uErr)
				require.NoError(t, txtRepo.Upsert(ctx, series.EpisodeText{
					EpisodeID: domain.EpisodeID(epIDRaw), Language: "ru-RU",
					Title: new(fmt.Sprintf("s1e%d ru", ep)),
				}))
			}
			// Season 2: 3 episodes, NO ru-RU rows.
			for ep := 1; ep <= 3; ep++ {
				_, uErr := epRepo.Upsert(ctx, series.CanonEpisode{
					SeriesID: seriesID, SeasonNumber: 2, EpisodeNumber: ep,
				})
				require.NoError(t, uErr)
			}

			// Per-season: season 1 fully covered, season 2 empty.
			cov1, tot1, err := txtRepo.CoverageBySeriesSeason(ctx, seriesID, 1, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 3, cov1)
			assert.Equal(t, 3, tot1, "season-1 denominator is scoped to season 1, not 6")

			cov2, tot2, err := txtRepo.CoverageBySeriesSeason(ctx, seriesID, 2, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 0, cov2)
			assert.Equal(t, 3, tot2)

			// Old series-wide verdict: 3/6 = 50% < 80% would mark season 1
			// stale; the new season-1 query is 100% and does not.
			covAll, totAll, err := txtRepo.CoverageBySeries(ctx, seriesID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 3, covAll)
			assert.Equal(t, 6, totAll)
			assert.True(t, covAll*100 < totAll*80, "series-wide would flag season 1 stale")
			assert.False(t, cov1*100 < tot1*80, "season-scoped season 1 is fresh")

			// NEGATIVE: a season with zero episodes → (0, 0, nil), no
			// divide-by-zero.
			covNone, totNone, err := txtRepo.CoverageBySeriesSeason(ctx, seriesID, 99, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 0, covNone)
			assert.Equal(t, 0, totNone)
		})
	}
}
