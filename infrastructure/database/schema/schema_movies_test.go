package schema

import "testing"

// Ф6-R-3 movie canon shape tests. One t.Run per dialect. Assert per table:
// exact column count, PK parts, FK symbol+reftable, and the UNIQUE/partial
// indexes by name. Mirrors TestSchema_DiscoveryBlocklist_Shape.

func TestSchema_Movies_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "movies")
			if got, want := len(tbl.Columns), 37; got != want {
				t.Fatalf("movies columns = %d, want %d", got, want)
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 ||
				tbl.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("movies PK = %+v, want single col id", tbl.PrimaryKey)
			}
			if len(tbl.ForeignKeys) != 0 {
				t.Errorf("movies has %d FKs, want 0", len(tbl.ForeignKeys))
			}
			var tmdbUnique bool
			idxByName := map[string]bool{}
			for _, ix := range tbl.Indexes {
				idxByName[ix.Name] = true
				if ix.Name == "movies_tmdb_id_idx" {
					tmdbUnique = ix.Unique
				}
			}
			if !tmdbUnique {
				t.Error("movies_tmdb_id_idx must be UNIQUE")
			}
			for _, want := range []string{
				"movies_tmdb_id_idx", "movies_imdb_id_idx",
				"movies_popularity_idx", "movies_tmdb_rating_idx",
				"movies_collection_id_idx", "movies_tmdb_changed_at_idx",
			} {
				if !idxByName[want] {
					t.Errorf("missing index %q", want)
				}
			}
		})
	}
}

func TestSchema_MovieI18n_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "movie_i18n")
			if got, want := len(tbl.Columns), 9; got != want {
				t.Fatalf("movie_i18n columns = %d, want %d", got, want)
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 ||
				tbl.PrimaryKey.Parts[0].C.Name != "movie_id" ||
				tbl.PrimaryKey.Parts[1].C.Name != "language" {
				t.Errorf("movie_i18n PK = %+v, want (movie_id, language)", tbl.PrimaryKey)
			}
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("movie_i18n has %d FKs, want 1", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.Symbol != "movie_i18n_movie_id_fkey" || fk.RefTable == nil || fk.RefTable.Name != "movies" {
				t.Errorf("movie_i18n FK = %q → %v, want movie_i18n_movie_id_fkey → movies", fk.Symbol, fk.RefTable)
			}
		})
	}
}

func TestSchema_MovieStates_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "movie_states")
			// 11 base + 4 downloaded-release facts (quality, resolution,
			// video_codec, audio_codec), all nullable.
			if got, want := len(tbl.Columns), 15; got != want {
				t.Fatalf("movie_states columns = %d, want %d", got, want)
			}
			colByName := map[string]bool{}
			for _, c := range tbl.Columns {
				colByName[c.Name] = true
			}
			for _, want := range []string{"quality", "resolution", "video_codec", "audio_codec"} {
				if !colByName[want] {
					t.Errorf("missing column %q", want)
				}
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 ||
				tbl.PrimaryKey.Parts[0].C.Name != "instance_name" ||
				tbl.PrimaryKey.Parts[1].C.Name != "radarr_movie_id" {
				t.Errorf("movie_states PK = %+v, want (instance_name, radarr_movie_id)", tbl.PrimaryKey)
			}
			// Exactly one FK → movies; NO FK on instance_name (app-managed cascade).
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("movie_states has %d FKs, want 1 (movie_id only, none on instance_name)", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.Symbol != "movie_states_movie_id_fkey" || fk.RefTable == nil || fk.RefTable.Name != "movies" {
				t.Errorf("movie_states FK = %q → %v, want movie_states_movie_id_fkey → movies", fk.Symbol, fk.RefTable)
			}
			idxByName := map[string]bool{}
			for _, ix := range tbl.Indexes {
				idxByName[ix.Name] = true
			}
			for _, want := range []string{"movie_states_instance_active", "movie_states_movie_id"} {
				if !idxByName[want] {
					t.Errorf("missing index %q", want)
				}
			}
		})
	}
}

func TestSchema_Collections_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "collections")
			if got, want := len(tbl.Columns), 10; got != want {
				t.Fatalf("collections columns = %d, want %d", got, want)
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 ||
				tbl.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("collections PK = %+v, want single col id", tbl.PrimaryKey)
			}
			if len(tbl.ForeignKeys) != 0 {
				t.Errorf("collections has %d FKs, want 0", len(tbl.ForeignKeys))
			}
			var uniq bool
			for _, ix := range tbl.Indexes {
				if ix.Name == "collections_tmdb_collection_id" {
					uniq = ix.Unique
					if len(ix.Parts) != 1 || ix.Parts[0].C.Name != "tmdb_collection_id" {
						t.Errorf("collections_tmdb_collection_id parts = %v, want [tmdb_collection_id]", ix.Parts)
					}
				}
			}
			if !uniq {
				t.Error("missing UNIQUE index collections_tmdb_collection_id")
			}
		})
	}
}
