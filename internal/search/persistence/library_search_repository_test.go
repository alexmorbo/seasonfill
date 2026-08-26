package persistence

import (
	"context"
	"fmt"
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

// seedCollection inserts a collection row (id = tmdb_collection_id for
// determinism). poster/backdrop may be nil.
func seedCollection(t *testing.T, db *gorm.DB, tmdbCollID int64, name string, poster, backdrop *string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO collections (id, tmdb_collection_id, name, poster_asset, backdrop_asset)
		 VALUES (?, ?, ?, ?, ?)`,
		tmdbCollID, tmdbCollID, name, poster, backdrop,
	).Error)
}

// seedPerson inserts a person + optional people_texts rows. people.name was
// dropped (000037) — never inserted. originalName == "" maps to SQL NULL so the
// NULL-original_name D-0 case is expressible.
func seedPerson(t *testing.T, db *gorm.DB, id, tmdbID int64, originalName string, popularity float64, knownFor string, texts map[string]string) {
	t.Helper()
	var on any
	if originalName != "" {
		on = originalName
	}
	require.NoError(t, db.Exec(
		`INSERT INTO people (id, tmdb_id, original_name, popularity, known_for_department)
		 VALUES (?, ?, ?, ?, ?)`,
		id, tmdbID, on, popularity, knownFor,
	).Error)
	for lang, name := range texts {
		require.NoError(t, db.Exec(
			`INSERT INTO people_texts (person_id, language, name) VALUES (?, ?, ?)`,
			id, lang, name,
		).Error)
	}
}

// seedPersonCredit inserts one person_credits row. mediaType ∈ {"movie","tv"};
// tmdbMediaID is the library title's tmdb_id for the D7 restriction. Supplies
// all NOT NULL columns (tmdb_credit_id, title, kind).
func seedPersonCredit(t *testing.T, db *gorm.DB, personID int64, mediaType string, tmdbMediaID int64) {
	t.Helper()
	creditID := fmt.Sprintf("c-%d-%s-%d", personID, mediaType, tmdbMediaID)
	require.NoError(t, db.Exec(
		`INSERT INTO person_credits (person_id, tmdb_credit_id, media_type, tmdb_media_id, title, kind)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		personID, creditID, mediaType, tmdbMediaID, "credit", "cast",
	).Error)
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

// ---------- COLLECTIONS (dual-backend) ----------

func TestSearchCollections_Match(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			seedCollection(t, db, 800, "The Matrix Collection", nil, nil)
			seedCollection(t, db, 801, "Unrelated Saga", nil, nil)

			got, err := repo.SearchCollections(ctx, "Matrix", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(800), int64(got[0].CollectionID))
			require.NotNil(t, got[0].TMDBID)
			assert.Equal(t, int64(800), int64(*got[0].TMDBID))
			assert.Equal(t, "The Matrix Collection", got[0].Name)
		})
	}
}

func TestSearchCollections_RankingAndLimit(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			seedCollection(t, db, 810, "Star Trek Collection", nil, nil)
			seedCollection(t, db, 811, "Star Wars Collection", nil, nil)
			seedCollection(t, db, 812, "Stargate Collection", nil, nil)

			got, err := repo.SearchCollections(ctx, "Star", "en-US", 2)
			require.NoError(t, err)
			require.Len(t, got, 2, "limit caps the group")
		})
	}
}

func TestSearchCollections_EmptyQuery(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			got, err := repo.SearchCollections(context.Background(), "   ", "en-US", 20)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

// ---------- PEOPLE (dual-backend, D7 library restriction) ----------

func TestSearchPeople_LibraryRestriction(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			// In-library movie (tmdb_id 900) + person credited on it.
			seedMovie(t, db, 900, "Some Film", "", 5.0, 2010, nil)
			seedPerson(t, db, 910, 910, "Jane Director", 20.0, "Directing", nil)
			seedPersonCredit(t, db, 910, "movie", 900)

			// Out-of-library person with the SAME name, credited only on a
			// tmdb_media_id that is NOT in any library title.
			seedPerson(t, db, 911, 911, "Jane Director", 99.0, "Directing", nil)
			seedPersonCredit(t, db, 911, "movie", 999999)

			got, err := repo.SearchPeople(ctx, "Jane Director", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1, "only the in-library person surfaces (D7)")
			assert.Equal(t, []int64{910}, personIDs(got))
			require.NotNil(t, got[0].KnownFor)
			assert.Equal(t, "Directing", *got[0].KnownFor)
		})
	}
}

// TestSearchPeople_UnionBothBranches proves the BUG-2 rewrite: the candidate
// set is the UNION of (original_name match) and (people_texts.name match). One
// in-library person matches ONLY via original_name, another ONLY via
// people_texts.name — both must surface (the JOIN matched CTE covers both
// branches without duplicating a person that matches in both).
func TestSearchPeople_UnionBothBranches(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			// One in-library movie both people are credited on.
			seedMovie(t, db, 1000, "Shared Film", "", 5.0, 2015, nil)

			// Person A matches ONLY via original_name (no people_texts).
			seedPerson(t, db, 1010, 1010, "Zephyrus Alpha", 10.0, "Acting", nil)
			seedPersonCredit(t, db, 1010, "movie", 1000)

			// Person B matches ONLY via people_texts.name (NULL original_name).
			seedPerson(t, db, 1011, 1011, "", 20.0, "Acting", map[string]string{"en-US": "Zephyrus Beta"})
			seedPersonCredit(t, db, 1011, "movie", 1000)

			got, err := repo.SearchPeople(ctx, "Zephyrus", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 2, "both UNION branches surface")
			assert.ElementsMatch(t, []int64{1010, 1011}, personIDs(got))
		})
	}
}

// TestSearchPeople_D7SoleWhereStillExcludes proves the D7 restriction, now the
// SOLE WHERE clause after the candidate set is materialized via the matched
// CTE, still admits an in-library person and excludes an identically-named
// person credited only outside the library (semantics byte-identical to the
// pre-rewrite EXISTS predicate).
func TestSearchPeople_D7SoleWhereStillExcludes(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			seedMovie(t, db, 1100, "Library Title", "", 5.0, 2012, nil)
			// In-library person — credited on the library title.
			seedPerson(t, db, 1110, 1110, "Nomen Sharedname", 15.0, "Acting", nil)
			seedPersonCredit(t, db, 1110, "movie", 1100)
			// Same-name person credited only on a tmdb_media_id NOT in the library.
			seedPerson(t, db, 1111, 1111, "Nomen Sharedname", 99.0, "Acting", nil)
			seedPersonCredit(t, db, 1111, "movie", 888888)

			got, err := repo.SearchPeople(ctx, "Sharedname", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1, "only the in-library person surfaces (D7 sole WHERE)")
			assert.Equal(t, []int64{1110}, personIDs(got))
		})
	}
}

func TestSearchPeople_NoCreditsExcluded(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			// Person matches by name but has NO person_credits → excluded.
			seedPerson(t, db, 920, 920, "Orphan Actor", 50.0, "Acting", nil)

			got, err := repo.SearchPeople(ctx, "Orphan Actor", "en-US", 20)
			require.NoError(t, err)
			assert.Empty(t, got, "person with no credits is excluded by D7")
		})
	}
}

func TestSearchPeople_NullOriginalNameResolvedViaTexts(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()

			// In-library tv title + person with NULL original_name whose only
			// name lives in people_texts (D-0 NULL pair).
			seedSeries(t, db, 930, "Some Show", 5.0, 2011, map[string]string{"en-US": "Some Show"})
			seedPerson(t, db, 940, 940, "", 10.0, "Acting", map[string]string{"en-US": "Named Via Texts"})
			seedPersonCredit(t, db, 940, "tv", 930)

			got, err := repo.SearchPeople(ctx, "Named Via Texts", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(940), int64(got[0].PersonID))
			assert.Equal(t, "Named Via Texts", got[0].Name, "display name resolved from people_texts")
		})
	}
}

func TestSearchPeople_EmptyQuery(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			got, err := repo.SearchPeople(context.Background(), "  ", "en-US", 20)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

// ---------- POSTGRES-ONLY LANE (people/collections accent + EXPLAIN) ----------

func TestSearchPostgres_PersonNameAccentInsensitive(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	repo := NewLibrarySearchRepository(db)
	ctx := context.Background()

	// In-library movie + accented in-library person; unaccented query matches.
	seedMovie(t, db, 950, "Concert Film", "", 5.0, 2016, nil)
	seedPerson(t, db, 960, 960, "Beyoncé", 90.0, "Acting", nil)
	seedPersonCredit(t, db, 960, "movie", 950)

	got, err := repo.SearchPeople(ctx, "Beyonce", "en-US", 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(960), int64(got[0].PersonID))
}

func TestSearchPostgres_CollectionAccentInsensitive(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	repo := NewLibrarySearchRepository(db)
	ctx := context.Background()

	seedCollection(t, db, 970, "Amélie Collection", nil, nil)

	got, err := repo.SearchCollections(ctx, "Amelie", "en-US", 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(970), int64(got[0].CollectionID))
}

func TestSearchPostgres_PersonOriginalNamePredicateUsesTrigramIndex(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	seedPerson(t, db, 980, 980, "Beyoncé", 90.0, "Acting", nil)

	// Byte-identical to the 000067 index expression on people.original_name.
	probe := `SELECT p.id FROM people p WHERE lower(f_unaccent(p.original_name)) LIKE lower(f_unaccent(?))`
	assertUsesIndex(t, db, probe, "%once%", "people_original_name_trgm_idx", "people")
}

func TestSearchPostgres_CollectionNamePredicateUsesTrigramIndex(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	seedCollection(t, db, 990, "Interstellar Collection", nil, nil)

	probe := `SELECT c.id FROM collections c WHERE lower(f_unaccent(c.name)) LIKE lower(f_unaccent(?))`
	assertUsesIndex(t, db, probe, "%stel%", "collections_name_trgm_idx", "collections")
}

// ---------- helpers ----------

func movieIDs(hits []searchdomain.MovieHit) []int64 {
	out := make([]int64, 0, len(hits))
	for _, h := range hits {
		out = append(out, int64(h.MovieID))
	}
	return out
}

func personIDs(hits []searchdomain.PersonHit) []int64 {
	out := make([]int64, 0, len(hits))
	for _, h := range hits {
		out = append(out, int64(h.PersonID))
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

// seedCollectionText inserts a collection_texts row. Empty name/overview → SQL
// NULL (so the NULL-side-table fallback is expressible). collection_texts.
// collection_id is the collections LOCAL PK, which seedCollection sets == the
// tmdb_collection_id passed. updated_at has a DEFAULT → omitted here.
func seedCollectionText(t *testing.T, db *gorm.DB, collectionID int64, lang, name, overview string) {
	t.Helper()
	var n, o any
	if name != "" {
		n = name
	}
	if overview != "" {
		o = overview
	}
	require.NoError(t, db.Exec(
		`INSERT INTO collection_texts (collection_id, language, name, overview) VALUES (?, ?, ?, ?)`,
		collectionID, lang, n, o,
	).Error)
}

func TestSearchCollections_LocalizedNameMatch(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()
			seedCollection(t, db, 1300, "The Matrix Collection", nil, nil)
			seedCollectionText(t, db, 1300, "ru-RU", "Матрица: Коллекция", "")
			seedCollection(t, db, 1301, "Unrelated Saga", nil, nil)
			got, err := repo.SearchCollections(ctx, "Матрица", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, got, 1, "ru query matches localized collection_texts name")
			assert.Equal(t, int64(1300), int64(got[0].CollectionID))
			assert.Equal(t, "Матрица: Коллекция", got[0].Name, "display name localized to ru")
		})
	}
}

func TestSearchCollections_CanonOnlyStillMatches(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()
			seedCollection(t, db, 1310, "Interstellar Collection", nil, nil)
			got, err := repo.SearchCollections(ctx, "Interstellar", "en-US", 20)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int64(1310), int64(got[0].CollectionID))
			assert.Equal(t, "Interstellar Collection", got[0].Name, "canon fallback when no collection_texts row")
		})
	}
}

func TestSearchCollections_DisplayNameFallbackLadder(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewLibrarySearchRepository(db)
			ctx := context.Background()
			seedCollection(t, db, 1320, "Dune Collection", nil, nil)
			seedCollectionText(t, db, 1320, "en-US", "Dune Collection", "")
			seedCollectionText(t, db, 1320, "ru-RU", "Дюна: Коллекция", "")
			seedCollection(t, db, 1321, "Alien Collection", nil, nil)
			seedCollectionText(t, db, 1321, "en-US", "Alien Saga (EN)", "")
			seedCollection(t, db, 1322, "Predator Collection", nil, nil)
			got1, err := repo.SearchCollections(ctx, "Dune", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, got1, 1)
			assert.Equal(t, "Дюна: Коллекция", got1[0].Name, "requested ru wins")
			got2, err := repo.SearchCollections(ctx, "Alien", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, got2, 1)
			assert.Equal(t, "Alien Saga (EN)", got2[0].Name, "en-US fallback when ru missing")
			got3, err := repo.SearchCollections(ctx, "Predator", "ru-RU", 20)
			require.NoError(t, err)
			require.Len(t, got3, 1)
			assert.Equal(t, "Predator Collection", got3[0].Name, "canon fallback when no texts")
		})
	}
}

func TestSearchPostgres_CollectionTextsPredicateUsesTrigramIndex(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	t.Parallel()
	pc := testhelpers.StartPostgres(t)
	db := pc.NewDB(t)
	seedCollection(t, db, 1330, "Interstellar Collection", nil, nil)
	seedCollectionText(t, db, 1330, "ru-RU", "Интерстеллар: Коллекция", "")
	probe := `SELECT ct.collection_id FROM collection_texts ct WHERE lower(f_unaccent(ct.name)) LIKE lower(f_unaccent(?))`
	assertUsesIndex(t, db, probe, "%стелл%", "collection_texts_name_trgm_idx", "collection_texts")
}
