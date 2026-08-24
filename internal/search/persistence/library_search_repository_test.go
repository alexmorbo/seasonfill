package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedSeries inserts a series + one series_texts row (language, title).
func seedSeries(t *testing.T, db *gorm.DB, tmdbID int64, originalTitle string, popularity float64, year int, texts map[string]string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO series (id, tmdb_id, original_title, popularity, year) VALUES (?, ?, ?, ?, ?)`,
		tmdbID, tmdbID, originalTitle, popularity, year,
	).Error)
	var id int64
	require.NoError(t, db.Raw(`SELECT id FROM series WHERE tmdb_id = ?`, tmdbID).Scan(&id).Error)
	for lang, title := range texts {
		require.NoError(t, db.Exec(
			`INSERT INTO series_texts (series_id, language, title) VALUES (?, ?, ?)`,
			id, lang, title,
		).Error)
	}
}

// seedMovie inserts a movie + optional movie_i18n rows.
func seedMovie(t *testing.T, db *gorm.DB, tmdbID int64, title, originalTitle string, popularity float64, year int, i18n map[string]string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO movies (id, tmdb_id, title, original_title, popularity, year, origin_countries)
		 VALUES (?, ?, ?, ?, ?, ?, '[]')`,
		tmdbID, tmdbID, title, originalTitle, popularity, year,
	).Error)
	var id int64
	require.NoError(t, db.Raw(`SELECT id FROM movies WHERE tmdb_id = ?`, tmdbID).Scan(&id).Error)
	for lang, t18 := range i18n {
		require.NoError(t, db.Exec(
			`INSERT INTO movie_i18n (movie_id, language, title) VALUES (?, ?, ?)`,
			id, lang, t18,
		).Error)
	}
}

// ---------- SERIES (dual-backend) ----------

func TestSearchSeries_MatchAcrossLanguages(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			// ru-RU localized title differs from the en-US canon.
			seedSeries(t, db, 100, "Mutiny", 9.0, 2020, map[string]string{
				"en-US": "Mutiny", "ru-RU": "Мятеж",
			})
			seedSeries(t, db, 101, "Unrelated", 1.0, 2019, map[string]string{"en-US": "Unrelated"})

			// English canon match.
			got, err := repo.SearchSeries(ctx, "Mutiny", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(100), int64(got[0].SeriesID))

			// Localized (Russian) match — case-identical so SQLite ASCII-fold
			// does not drop it; on Postgres this is a trigram match.
			got, err = repo.SearchSeries(ctx, "Мятеж", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(100), int64(got[0].SeriesID))
			// Display title resolves to the requested language.
			assert.Equal(t, "Мятеж", got[0].Title)
		})
	}
}

func TestSearchSeries_RankingAndLimit(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			seedSeries(t, db, 200, "Star Trek", 5.0, 1966, map[string]string{"en-US": "Star Trek"})
			seedSeries(t, db, 201, "Star Wars Rebels", 50.0, 2014, map[string]string{"en-US": "Star Wars Rebels"})
			seedSeries(t, db, 202, "Star Gate", 20.0, 1997, map[string]string{"en-US": "Star Gate"})

			got, err := repo.SearchSeries(ctx, "Star", "en-US", 2)
			require.NoError(t, err)
			require.Len(t, got, 2, "limit caps the group")
		})
	}
}

func TestSearchSeries_EmptyQuery(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			got, err := repo.SearchSeries(context.Background(), "   ", "en-US", 20)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

// ---------- MOVIES (dual-backend) ----------

func TestSearchMovies_AdditivePredicate(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			// Canon EN "Mutiny", ru-RU i18n "Мятеж", original_title "Bunt".
			seedMovie(t, db, 300, "Mutiny", "Bunt", 8.0, 2021, map[string]string{"ru-RU": "Мятеж"})
			// Unenriched movie: only canon title, no i18n (F-03 findability).
			seedMovie(t, db, 301, "Plain Movie", "", 2.0, 2018, nil)

			// canon
			got, err := repo.SearchMovies(ctx, "Mutiny", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(300), int64(got[0].MovieID))

			// original_title
			got, err = repo.SearchMovies(ctx, "Bunt", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(300), int64(got[0].MovieID))

			// localized i18n
			got, err = repo.SearchMovies(ctx, "Мятеж", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(300), int64(got[0].MovieID))
			assert.Equal(t, "Мятеж", got[0].Title) // display resolves ru-RU

			// unenriched still findable via canon title
			got, err = repo.SearchMovies(ctx, "Plain", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(301), int64(got[0].MovieID))
		})
	}
}

func TestSearchMovies_NullFields(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			// No year, no poster/backdrop, no i18n — all NULL.
			require.NoError(t, db.Exec(
				`INSERT INTO movies (tmdb_id, title, origin_countries) VALUES (?, ?, '[]')`,
				400, "Nullmovie",
			).Error)

			got, err := repo.SearchMovies(ctx, "Nullmovie", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "Nullmovie", got[0].Title)
			assert.Nil(t, got[0].Year)
			assert.Nil(t, got[0].PosterPath)
			assert.Nil(t, got[0].BackdropPath)
		})
	}
}

// ---------- POSTGRES-ONLY LANE (accent, min-length, EXPLAIN) ----------

func TestSearchPostgres_AccentInsensitive(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	repo := NewLibrarySearchRepository(db)
	ctx := context.Background()

	// Accented original_title; unaccented query must match via f_unaccent.
	seedMovie(t, db, 500, "Amelie", "Amélie", 30.0, 2001, nil)

	got, err := repo.SearchMovies(ctx, "Amelie", "en-US", 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(500), int64(got[0].MovieID))

	// And the reverse: accented query matches the unaccented canon title.
	got, err = repo.SearchMovies(ctx, "Amélie", "en-US", 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestSearchPostgres_MinLengthBranch(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	repo := NewLibrarySearchRepository(db)
	ctx := context.Background()

	// "Rome" prefix-matches "ro"; "Zorro" only substring-contains "orr"/"ro".
	seedMovie(t, db, 600, "Rome", "Rome", 10.0, 2005, nil)
	seedMovie(t, db, 601, "Zorro", "Zorro", 9.0, 1998, nil)

	// q len 2 -> PREFIX branch: only titles STARTING with "ro".
	got, err := repo.SearchMovies(ctx, "ro", "en-US", 20)
	require.NoError(t, err)
	ids := movieIDs(got)
	assert.Contains(t, ids, int64(600), "Rome starts with 'ro'")
	assert.NotContains(t, ids, int64(601), "Zorro does not start with 'ro' (prefix branch)")

	// q len 3 -> SUBSTRING/trigram branch: "orr" is inside "Zorro".
	got, err = repo.SearchMovies(ctx, "orr", "en-US", 20)
	require.NoError(t, err)
	ids = movieIDs(got)
	assert.Contains(t, ids, int64(601), "Zorro contains 'orr' (substring branch)")
}

func TestSearchPostgres_MovieTitlePredicateUsesTrigramIndex(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	seedMovie(t, db, 700, "Interstellar", "Interstellar", 40.0, 2014, nil)

	// Isolated predicate probe (byte-identical to the 000067 index expression).
	probe := `SELECT m.id FROM movies m WHERE lower(f_unaccent(m.title)) LIKE lower(f_unaccent(?))`
	assertUsesIndex(t, db, probe, "%stel%", "movies_title_trgm_idx", "movies")
}

func TestSearchPostgres_SeriesTextsPredicateUsesTrigramIndex(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	seedSeries(t, db, 701, "Foundation", 40.0, 2021, map[string]string{"en-US": "Foundation"})

	probe := `SELECT s.id FROM series s
 WHERE EXISTS (SELECT 1 FROM series_texts st WHERE st.series_id = s.id
                 AND lower(f_unaccent(st.title)) LIKE lower(f_unaccent(?)))`
	assertUsesIndex(t, db, probe, "%ound%", "series_texts_title_trgm_idx", "series_texts")
}

// ---------- helpers ----------

func movieIDs(hits []searchdomain.MovieHit) []int64 {
	out := make([]int64, 0, len(hits))
	for _, h := range hits {
		out = append(out, int64(h.MovieID))
	}
	return out
}

// assertUsesIndex proves the predicate is byte-compatible with the trgm GIN
// index. On tiny testcontainer tables the cost planner prefers Seq Scan, so we
// disable seqscan for the statement: with it off, the GIN index is used IFF
// the predicate expression matches the index expression. We assert the plan
// mentions the index and does NOT Seq-Scan the matched table.
func assertUsesIndex(t *testing.T, db *gorm.DB, probeSQL, arg, indexName, matchedTable string) {
	t.Helper()
	require.NoError(t, db.Exec(`SET enable_seqscan = off`).Error)
	defer func() { _ = db.Exec(`SET enable_seqscan = on`).Error }()

	var lines []string
	require.NoError(t, db.Raw(`EXPLAIN `+probeSQL, arg).Scan(&lines).Error)
	plan := strings.Join(lines, "\n")

	assert.Contains(t, plan, indexName,
		"predicate must use the trgm GIN index (byte-identity check)\nplan:\n%s", plan)
	assert.NotContains(t, plan, "Seq Scan on "+matchedTable,
		"predicate must not seq-scan %s when the index applies\nplan:\n%s", matchedTable, plan)
}
