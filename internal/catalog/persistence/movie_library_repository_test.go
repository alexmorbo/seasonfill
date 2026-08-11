package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedRichMovie upserts a fuller movies canon row and returns its id.
func seedRichMovie(t *testing.T, db *gorm.DB, tmdbID int, title string, year int, release time.Time, tmdbRating float64) domain.MovieID {
	t.Helper()
	y := year
	rel := release
	rating := tmdbRating
	poster := "/poster_" + title + ".jpg"
	id, err := enrichpersistence.NewMovieRepository(db).Upsert(context.Background(), movie.Canon{
		TMDBID: tmdbIDPtr(tmdbID), Hydration: movie.HydrationFull, Title: title,
		Year: &y, ReleaseDate: &rel, TMDBRating: &rating,
		PosterAsset: &poster,
	})
	require.NoError(t, err)
	return id
}

func addMembership(t *testing.T, db *gorm.DB, instance string, radarrID int, movieID domain.MovieID, monitored, hasFile bool, size int64, updated time.Time) {
	t.Helper()
	require.NoError(t, NewMovieStatesRepository(db).Upsert(context.Background(), movie.StateEntry{
		InstanceName: domain.InstanceName(instance), RadarrMovieID: radarrID, MovieID: movieID,
		TitleSlug: "slug", Monitored: monitored, HasFile: hasFile, SizeOnDiskBytes: size,
		AddedToRadarr: true, UpdatedAt: updated,
	}))
}

func TestMovieLibraryRepository_List(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMovieLibraryRepository(db)
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			// A — downloaded on r1 (has_file). Newest release, oldest updated.
			a := seedRichMovie(t, db, 100, "Alpha", 2024, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), 8.1)
			addMembership(t, db, "r1", 1, a, true, true, 5_000_000_000, base.Add(1*time.Hour))
			// B — missing on r1 (monitored, no file). Oldest release, newest updated.
			b := seedRichMovie(t, db, 200, "Bravo", 2020, time.Date(2020, 3, 1, 0, 0, 0, 0, time.UTC), 6.5)
			addMembership(t, db, "r1", 2, b, true, false, 0, base.Add(3*time.Hour))
			// C — multi-instance: r1 (no file) + r2 (has file) → downloaded (OR).
			c := seedRichMovie(t, db, 300, "Charlie", 2022, time.Date(2022, 9, 1, 0, 0, 0, 0, time.UTC), 7.2)
			addMembership(t, db, "r1", 3, c, false, false, 1_000, base.Add(2*time.Hour))
			addMembership(t, db, "r2", 9, c, true, true, 9_000_000_000, base.Add(2*time.Hour))

			t.Run("all_dedup_and_aggregate", func(t *testing.T) {
				rows, total, err := repo.List(ctx, ports.MovieLibraryFilter{State: ports.MovieLibraryStateAll}, ports.MovieLibrarySortTitleAsc, 50, 0)
				require.NoError(t, err)
				assert.Equal(t, 3, total, "3 distinct movies despite C having 2 memberships")
				require.Len(t, rows, 3)
				byT := map[int]ports.MovieLibraryRow{}
				for _, r := range rows {
					byT[r.TMDBID] = r
				}
				// C aggregated: both instances listed, OR'd has_file/monitored, MAX size.
				cc := byT[300]
				assert.ElementsMatch(t, []string{"r1", "r2"}, cc.Instances)
				assert.True(t, cc.HasFile, "OR across instances → has_file")
				assert.True(t, cc.Monitored)
				assert.Equal(t, int64(9_000_000_000), cc.SizeOnDisk, "largest copy")
				assert.Equal(t, "Charlie", cc.Title)
				require.NotNil(t, cc.PosterAsset)
				require.NotNil(t, cc.TMDBRating)
				assert.InDelta(t, 7.2, *cc.TMDBRating, 0.001)
			})

			t.Run("state_downloaded", func(t *testing.T) {
				rows, total, err := repo.List(ctx, ports.MovieLibraryFilter{State: ports.MovieLibraryStateDownloaded}, ports.MovieLibrarySortTitleAsc, 50, 0)
				require.NoError(t, err)
				assert.Equal(t, 2, total, "A + C have files; B does not")
				got := []int{}
				for _, r := range rows {
					got = append(got, r.TMDBID)
				}
				assert.ElementsMatch(t, []int{100, 300}, got)
			})

			t.Run("state_missing", func(t *testing.T) {
				rows, total, err := repo.List(ctx, ports.MovieLibraryFilter{State: ports.MovieLibraryStateMissing}, ports.MovieLibrarySortTitleAsc, 50, 0)
				require.NoError(t, err)
				require.Equal(t, 1, total)
				require.Len(t, rows, 1)
				assert.Equal(t, 200, rows[0].TMDBID, "only B: monitored + no file anywhere")
			})

			t.Run("sort_title_asc", func(t *testing.T) {
				rows, _, err := repo.List(ctx, ports.MovieLibraryFilter{}, ports.MovieLibrarySortTitleAsc, 50, 0)
				require.NoError(t, err)
				require.Len(t, rows, 3)
				assert.Equal(t, []int{100, 200, 300}, []int{rows[0].TMDBID, rows[1].TMDBID, rows[2].TMDBID})
			})

			t.Run("sort_release_desc", func(t *testing.T) {
				rows, _, err := repo.List(ctx, ports.MovieLibraryFilter{}, ports.MovieLibrarySortReleaseDesc, 50, 0)
				require.NoError(t, err)
				require.Len(t, rows, 3)
				// Alpha 2024 > Charlie 2022 > Bravo 2020
				assert.Equal(t, []int{100, 300, 200}, []int{rows[0].TMDBID, rows[1].TMDBID, rows[2].TMDBID})
			})

			t.Run("sort_updated_desc", func(t *testing.T) {
				rows, _, err := repo.List(ctx, ports.MovieLibraryFilter{}, ports.MovieLibrarySortUpdatedDesc, 50, 0)
				require.NoError(t, err)
				require.Len(t, rows, 3)
				// MovieStatesRepository.Upsert stamps updated_at = now(), so the
				// MAX(updated_at) per movie follows insert order: A first, then B,
				// then C (last touched). updated_desc → C, B, A.
				assert.Equal(t, []int{300, 200, 100}, []int{rows[0].TMDBID, rows[1].TMDBID, rows[2].TMDBID})
			})

			t.Run("pagination", func(t *testing.T) {
				page1, total, err := repo.List(ctx, ports.MovieLibraryFilter{}, ports.MovieLibrarySortTitleAsc, 2, 0)
				require.NoError(t, err)
				assert.Equal(t, 3, total)
				require.Len(t, page1, 2)
				assert.Equal(t, []int{100, 200}, []int{page1[0].TMDBID, page1[1].TMDBID})
				page2, total2, err := repo.List(ctx, ports.MovieLibraryFilter{}, ports.MovieLibrarySortTitleAsc, 2, 2)
				require.NoError(t, err)
				assert.Equal(t, 3, total2)
				require.Len(t, page2, 1)
				assert.Equal(t, 300, page2[0].TMDBID)
			})

			t.Run("search", func(t *testing.T) {
				rows, total, err := repo.List(ctx, ports.MovieLibraryFilter{Search: "brav"}, ports.MovieLibrarySortTitleAsc, 50, 0)
				require.NoError(t, err)
				require.Equal(t, 1, total)
				require.Len(t, rows, 1)
				assert.Equal(t, 200, rows[0].TMDBID)
			})

			t.Run("soft_deleted_excluded", func(t *testing.T) {
				// Soft-delete C on r2 (the only copy with a file); C should now be missing-eligible via r1? r1 is unmonitored+nofile → not missing, not downloaded → still in "all".
				require.NoError(t, NewMovieStatesRepository(db).SoftDelete(ctx, "r2", 9))
				rows, total, err := repo.List(ctx, ports.MovieLibraryFilter{State: ports.MovieLibraryStateDownloaded}, ports.MovieLibrarySortTitleAsc, 50, 0)
				require.NoError(t, err)
				assert.Equal(t, 1, total, "after r2 soft-delete only A remains downloaded")
				require.Len(t, rows, 1)
				assert.Equal(t, 100, rows[0].TMDBID)
				// C still present in "all" via r1 membership, now single-instance.
				allRows, allTotal, err := repo.List(ctx, ports.MovieLibraryFilter{}, ports.MovieLibrarySortTitleAsc, 50, 0)
				require.NoError(t, err)
				assert.Equal(t, 3, allTotal)
				for _, r := range allRows {
					if r.TMDBID == 300 {
						assert.Equal(t, []string{"r1"}, r.Instances)
					}
				}
			})
		})
	}
}

func TestMovieLibraryRepository_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			rows, total, err := NewMovieLibraryRepository(db).List(context.Background(),
				ports.MovieLibraryFilter{}, ports.MovieLibrarySortUpdatedDesc, 24, 0)
			require.NoError(t, err)
			assert.Equal(t, 0, total)
			assert.Empty(t, rows)
		})
	}
}
