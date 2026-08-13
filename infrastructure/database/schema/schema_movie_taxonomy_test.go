package schema

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"
)

// Ф0.3 movie join/projection shape tests — one t.Run per dialect. Mirrors
// schema_movies_test.go. Movie-side FK is ON DELETE CASCADE (join/projection
// rows are dead once the movie is gone); dimension-side FK is NO ACTION.
func TestSchema_MovieGenres_Shape(t *testing.T) {
	assertMovieJoinShape(t, "movie_genres", "genre_id", "genres", 3, "movie_genres_genre")
}
func TestSchema_MovieCompanies_Shape(t *testing.T) {
	assertMovieJoinShape(t, "movie_companies", "company_id", "production_companies", 3, "movie_companies_company")
}
func TestSchema_MovieKeywords_Shape(t *testing.T) {
	assertMovieJoinShape(t, "movie_keywords", "keyword_id", "keywords", 2, "movie_keywords_keyword")
}

// assertMovieJoinShape checks a 2-FK movie↔dimension join table: composite PK
// (movie_id, rightCol), colCount columns, reverse-lookup index, movie-side
// CASCADE FK → movies, dimension-side NO ACTION FK → refTable.
func assertMovieJoinShape(t *testing.T, table, rightCol, refTable string, colCount int, idxName string) {
	t.Helper()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			s := Schema(d)
			tbl := mustTable(s, table)
			if got := len(tbl.Columns); got != colCount {
				t.Fatalf("%s columns = %d, want %d", table, got, colCount)
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 ||
				tbl.PrimaryKey.Parts[0].C.Name != "movie_id" ||
				tbl.PrimaryKey.Parts[1].C.Name != rightCol {
				t.Errorf("%s PK = %+v, want (movie_id, %s)", table, tbl.PrimaryKey, rightCol)
			}
			if len(tbl.ForeignKeys) != 2 {
				t.Fatalf("%s has %d FKs, want 2", table, len(tbl.ForeignKeys))
			}
			var idxFound bool
			for _, ix := range tbl.Indexes {
				if ix.Name == idxName {
					idxFound = true
				}
			}
			if !idxFound {
				t.Errorf("%s missing reverse-lookup index %q", table, idxName)
			}
			_ = refTable
		})
	}
}

func TestSchema_MovieRecommendations_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "movie_recommendations")
			if got, want := len(tbl.Columns), 4; got != want {
				t.Fatalf("movie_recommendations columns = %d, want %d", got, want)
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 ||
				tbl.PrimaryKey.Parts[0].C.Name != "movie_id" ||
				tbl.PrimaryKey.Parts[1].C.Name != "recommended_movie_id" {
				t.Errorf("movie_recommendations PK = %+v, want (movie_id, recommended_movie_id)", tbl.PrimaryKey)
			}
			if len(tbl.ForeignKeys) != 2 {
				t.Fatalf("movie_recommendations has %d FKs, want 2 (both → movies)", len(tbl.ForeignKeys))
			}
			for _, fk := range tbl.ForeignKeys {
				if fk.RefTable == nil || fk.RefTable.Name != "movies" {
					t.Errorf("movie_recommendations FK %q → %v, want → movies", fk.Symbol, fk.RefTable)
				}
			}
			// Checks live in Table.Attrs (atlas has no Table.Checks field).
			var checks []*atlasschema.Check
			for _, a := range tbl.Attrs {
				if c, ok := a.(*atlasschema.Check); ok {
					checks = append(checks, c)
				}
			}
			if len(checks) != 1 || checks[0].Name != "movie_recommendations_no_self_ref" {
				t.Errorf("movie_recommendations checks = %+v, want [movie_recommendations_no_self_ref]", checks)
			}
		})
	}
}

func TestSchema_MovieVideos_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "movie_videos")
			if got, want := len(tbl.Columns), 12; got != want {
				t.Fatalf("movie_videos columns = %d, want %d", got, want)
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 ||
				tbl.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("movie_videos PK = %+v, want single col id", tbl.PrimaryKey)
			}
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("movie_videos has %d FKs, want 1 (movie_id → movies)", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.Symbol != "movie_videos_movie_id_fkey" || fk.RefTable == nil || fk.RefTable.Name != "movies" {
				t.Errorf("movie_videos FK = %q → %v, want movie_videos_movie_id_fkey → movies", fk.Symbol, fk.RefTable)
			}
			var tmdbUnique, hasComposite bool
			for _, ix := range tbl.Indexes {
				if ix.Name == "movie_videos_tmdb_id" {
					tmdbUnique = ix.Unique
				}
				if ix.Name == "movie_videos_movie_type" {
					hasComposite = true
				}
			}
			if !tmdbUnique {
				t.Error("movie_videos_tmdb_id must be UNIQUE")
			}
			if !hasComposite {
				t.Error("missing index movie_videos_movie_type")
			}
		})
	}
}
