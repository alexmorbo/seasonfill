package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// mkMovie inserts a canon movie row and returns its id (movie_* joins FK movie_id → movies).
func mkMovie(t *testing.T, repo *MovieRepository, tmdbID int, title string) domain.MovieID {
	t.Helper()
	id, err := repo.Upsert(context.Background(), movie.Canon{
		TMDBID:    new(domain.TMDBID(tmdbID)),
		Hydration: movie.HydrationFull,
		Title:     title,
	})
	require.NoError(t, err)
	require.NotZero(t, id)
	return id
}

func TestGenresRepository_SetMovie(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := NewMovieRepository(db)
			genres := NewGenresRepository(db)
			i18n := NewGenresI18nRepository(db)

			movieID := mkMovie(t, movies, 693134, "Dune: Part Two")

			// Worker-equivalent seed: parent dict by tmdb_id + base-lang i18n.
			g1, err := genres.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(18)})
			require.NoError(t, err)
			g2, err := genres.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(878)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{GenreID: g1, Language: "en-US", Name: "Drama"}))
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{GenreID: g2, Language: "en-US", Name: "Science Fiction"}))

			require.NoError(t, genres.SetMovie(ctx, movieID, []int64{g1, g2}))

			// join rows present, position-ordered.
			var rows []struct {
				GenreID  int64
				Position *int
			}
			require.NoError(t, db.Table("movie_genres").
				Where("movie_id = ?", movieID).Order("position ASC").
				Find(&rows).Error)
			require.Len(t, rows, 2)
			assert.Equal(t, g1, rows[0].GenreID)
			require.NotNil(t, rows[0].Position)
			assert.Equal(t, 0, *rows[0].Position)
			assert.Equal(t, g2, rows[1].GenreID)

			// base-lang genres_i18n row present (i18n-row-present assertion).
			got, err := genres.Get(ctx, g1, "en-US")
			require.NoError(t, err)
			assert.Equal(t, "Drama", got.Name)
			assert.Equal(t, "en-US", got.Language)

			// replace: SetMovie is authoritative (DELETE+INSERT).
			require.NoError(t, genres.SetMovie(ctx, movieID, []int64{g2}))
			var cnt int64
			require.NoError(t, db.Table("movie_genres").Where("movie_id = ?", movieID).Count(&cnt).Error)
			assert.EqualValues(t, 1, cnt)

			// empty clears.
			require.NoError(t, genres.SetMovie(ctx, movieID, nil))
			require.NoError(t, db.Table("movie_genres").Where("movie_id = ?", movieID).Count(&cnt).Error)
			assert.EqualValues(t, 0, cnt)
		})
	}
}

func TestKeywordsRepository_SetMovie(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := NewMovieRepository(db)
			keywords := NewKeywordsRepository(db)
			i18n := NewKeywordsI18nRepository(db)

			movieID := mkMovie(t, movies, 42, "Dune")

			k1, err := keywords.Upsert(ctx, taxonomy.Keyword{TMDBID: ptrTMDBID(4565)})
			require.NoError(t, err)
			k2, err := keywords.Upsert(ctx, taxonomy.Keyword{TMDBID: ptrTMDBID(9951)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.KeywordI18n{KeywordID: k1, Language: "en-US", Name: "dystopia"}))

			// dedup guard: duplicate id must not trip the composite PK.
			require.NoError(t, keywords.SetMovie(ctx, movieID, []int64{k1, k1, k2}))

			var cnt int64
			require.NoError(t, db.Table("movie_keywords").Where("movie_id = ?", movieID).Count(&cnt).Error)
			assert.EqualValues(t, 2, cnt, "dedup collapses the duplicate keyword id")

			// movie_keywords has NO position column — assert the row selects with only the two PK cols.
			var kids []int64
			require.NoError(t, db.Table("movie_keywords").
				Where("movie_id = ?", movieID).Order("keyword_id ASC").
				Pluck("keyword_id", &kids).Error)
			assert.Equal(t, []int64{k1, k2}, kids)

			// base-lang keywords_i18n row present.
			got, err := keywords.Get(ctx, k1, "en-US")
			require.NoError(t, err)
			assert.Equal(t, "dystopia", got.Name)

			require.NoError(t, keywords.SetMovie(ctx, movieID, nil))
			require.NoError(t, db.Table("movie_keywords").Where("movie_id = ?", movieID).Count(&cnt).Error)
			assert.EqualValues(t, 0, cnt)
		})
	}
}

func TestCompaniesRepository_SetMovie(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := NewMovieRepository(db)
			companies := NewCompaniesRepository(db)

			movieID := mkMovie(t, movies, 7, "Movie")

			c1, err := companies.Upsert(ctx, taxonomy.ProductionCompany{TMDBID: ptrTMDBID(923), Name: "Legendary"})
			require.NoError(t, err)
			c2, err := companies.Upsert(ctx, taxonomy.ProductionCompany{TMDBID: ptrTMDBID(33), Name: "Universal"})
			require.NoError(t, err)

			require.NoError(t, companies.SetMovie(ctx, movieID, []int64{c1, c2}))

			var rows []struct {
				CompanyID int64
				Position  *int
			}
			require.NoError(t, db.Table("movie_companies").
				Where("movie_id = ?", movieID).Order("position ASC").
				Find(&rows).Error)
			require.Len(t, rows, 2)
			assert.Equal(t, c1, rows[0].CompanyID)
			require.NotNil(t, rows[0].Position)
			assert.Equal(t, 0, *rows[0].Position)

			// zero movie_id rejected.
			require.Error(t, companies.SetMovie(ctx, 0, []int64{c1}))
		})
	}
}
