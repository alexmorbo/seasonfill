// In-package coverage harness for schema.go.
//
// Cross-package smoke lives in /tests/d1_2_core_series_test.go and pins the
// shape contract. That suite exercises every helper transitively but its
// coverage is recorded against the `tests` package, not this one — leaving
// schema.go at 0.0% in the unit-job coverage profile despite being fully
// exercised (95.6% under `-coverpkg`). This file invokes the same public
// surface in-package so the helpers register in their own profile.
package schema

import (
	"os"
	"testing"
)

// TestSchemaCoverage_BothDialects walks Schema(d) for every shipped
// dialect. Touches every builder + helper transitively. Total table
// count is 24 after D-1-4b: 3 core + 2 i18n + 4 canon + 4 taxonomy_i18n
// + 4 joins + 3 people (people, person_credits, person_biographies)
// + 4 series_extras (videos, content_ratings, external_ids,
// series_recommendations).
func TestSchemaCoverage_BothDialects(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			if s == nil {
				t.Fatalf("Schema(%q) returned nil", d)
			}
			if len(s.Tables) != 24 {
				t.Fatalf("Schema(%q) tables = %d, want 24", d, len(s.Tables))
			}
		})
	}
}

// TestSchemaCoverage_TaxonomySkipFlag covers the ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS
// env branch in Schema(d). When set, the 4 join tables are skipped (used
// at dev-time to split the 000003_taxonomy migration from 000004_taxonomy_joins);
// when unset, all 24 tables are present (the prod path). 24 - 4 = 20.
func TestSchemaCoverage_TaxonomySkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 20 {
		t.Fatalf("Schema(postgres) with skip flag tables = %d, want 20 (24 - 4 joins)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "series_genres", "series_networks", "series_companies", "series_keywords":
			t.Errorf("join table %q should be skipped when ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_PeopleSkipFlag covers the ATLAS_SCHEMA_SKIP_PEOPLE
// env branch in Schema(d). When set, the 3 people-domain tables are
// skipped (used at dev-time to split the 000005_people migration from
// pre-existing migrations); when unset, all 24 tables are present (the
// prod path). 24 default - 3 (skipped people) = 21.
func TestSchemaCoverage_PeopleSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_PEOPLE", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 21 {
		t.Fatalf("Schema(postgres) with skip people tables = %d, want 21 (24 - 3 people)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "people", "person_credits", "person_biographies":
			t.Errorf("people-domain table %q should be skipped when ATLAS_SCHEMA_SKIP_PEOPLE is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_SeriesExtrasSkipFlag covers the
// ATLAS_SCHEMA_SKIP_SERIES_EXTRAS env branch in Schema(d). When set, the
// 4 series-extras tables (videos, content_ratings, external_ids,
// series_recommendations) are skipped (used at dev-time to split the
// 000006_series_extras migration from pre-existing migrations); when
// unset, all 24 tables are present (the prod path). 24 - 4 = 20.
func TestSchemaCoverage_SeriesExtrasSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_SERIES_EXTRAS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 20 {
		t.Fatalf("Schema(postgres) with skip series_extras tables = %d, want 20 (24 - 4 extras)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "videos", "content_ratings", "external_ids", "series_recommendations":
			t.Errorf("series_extras table %q should be skipped when ATLAS_SCHEMA_SKIP_SERIES_EXTRAS is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_LoadDefaultsToPostgres covers the Load() env-driven
// entrypoint with an unset ATLAS_DIALECT — the default branch.
func TestSchemaCoverage_LoadDefaultsToPostgres(t *testing.T) {
	t.Setenv(EnvDialect, "")
	s := Load()
	if s == nil {
		t.Fatal("Load() with empty env returned nil")
	}
	if s.Name != SchemaName {
		t.Errorf("Load() schema name = %q, want %q", s.Name, SchemaName)
	}
}

// TestSchemaCoverage_LoadHonorsEnv covers the env-set branch of Load()
// — drives the SQLite dispatch path explicitly.
func TestSchemaCoverage_LoadHonorsEnv(t *testing.T) {
	t.Setenv(EnvDialect, string(DialectSQLite))
	if got := os.Getenv(EnvDialect); got != string(DialectSQLite) {
		t.Fatalf("env setup botched: ATLAS_DIALECT=%q", got)
	}
	s := Load()
	if s == nil {
		t.Fatal("Load() returned nil with ATLAS_DIALECT=sqlite")
	}
	if len(s.Tables) != 24 {
		t.Fatalf("Load() tables = %d, want 24", len(s.Tables))
	}
}

// TestSchemaCoverage_UnknownDialectPanics covers the panic branch of
// Schema(d) — guards against silent empty-schema emission on typo.
func TestSchemaCoverage_UnknownDialectPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("Schema(\"mariadb\") did not panic")
		}
	}()
	_ = Schema("mariadb")
}

// TestSchemaCoverage_I18nNameLookupMissingNamePanic — i18nTextTable
// panics when the caller asks for a (language, name) lookup index but
// extraCols has no "name" column. Programmer error; we want the panic
// to fire loud rather than emit a broken index.
func TestSchemaCoverage_I18nNameLookupMissingNamePanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("i18nTextTable with nameLookupIdx and no name col did not panic")
		}
	}()
	// Build a stub parent table with a PK so parentRefCol succeeds.
	parent := buildGenresTable(DialectPostgres)
	_ = i18nTextTable(DialectPostgres, "stub_i18n", parent, "genre_id",
		nil, // no extraCols → no "name"
		"stub_lookup",
		false,
	)
}

// TestSchemaCoverage_MustTablePanic — mustTable panics when the named
// table is absent (programmer error — wrong appender order).
func TestSchemaCoverage_MustTablePanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("mustTable on missing name did not panic")
		}
	}()
	s := Schema(DialectPostgres)
	_ = mustTable(s, "nonexistent_table_name")
}
