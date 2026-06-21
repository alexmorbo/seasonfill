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
// count is 39 after D-1-7b: 36 prior (D-1-7a) + 3 grab tables
// (grab_records, episode_grabs, download_links).
func TestSchemaCoverage_BothDialects(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			if s == nil {
				t.Fatalf("Schema(%q) returned nil", d)
			}
			if len(s.Tables) != 39 {
				t.Fatalf("Schema(%q) tables = %d, want 39", d, len(s.Tables))
			}
		})
	}
}

// TestSchemaCoverage_TaxonomySkipFlag covers the ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS
// env branch in Schema(d). When set, the 4 join tables are skipped (used
// at dev-time to split the 000003_taxonomy migration from 000004_taxonomy_joins);
// when unset, all 39 tables are present (the prod path). 39 - 4 = 35.
func TestSchemaCoverage_TaxonomySkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 35 {
		t.Fatalf("Schema(postgres) with skip flag tables = %d, want 35 (39 - 4 joins)", len(s.Tables))
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
// pre-existing migrations); when unset, all 39 tables are present (the
// prod path). 39 default - 3 (skipped people) = 36.
func TestSchemaCoverage_PeopleSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_PEOPLE", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 36 {
		t.Fatalf("Schema(postgres) with skip people tables = %d, want 36 (39 - 3 people)", len(s.Tables))
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
// unset, all 39 tables are present (the prod path). 39 - 4 = 35.
func TestSchemaCoverage_SeriesExtrasSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_SERIES_EXTRAS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 35 {
		t.Fatalf("Schema(postgres) with skip series_extras tables = %d, want 35 (39 - 4 extras)", len(s.Tables))
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
	if len(s.Tables) != 39 {
		t.Fatalf("Load() tables = %d, want 39", len(s.Tables))
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

// TestSchemaCoverage_InstanceProjectionsSkipFlag covers the
// ATLAS_SCHEMA_SKIP_INSTANCE_PROJECTIONS env branch in Schema(d). When
// set, the 3 per-instance projection tables (series_cache,
// episode_states, season_stats) are skipped (dev-time split to generate
// 000007 cleanly); when unset, all 39 tables are present (the prod
// path). 39 - 3 = 36.
func TestSchemaCoverage_InstanceProjectionsSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_INSTANCE_PROJECTIONS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 36 {
		t.Fatalf("Schema(postgres) with skip projections tables = %d, want 36 (39 - 3 projections)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "series_cache", "episode_states", "season_stats":
			t.Errorf("instance-projection table %q should be skipped when ATLAS_SCHEMA_SKIP_INSTANCE_PROJECTIONS is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_EnrichmentTrackingSkipFlag covers the
// ATLAS_SCHEMA_SKIP_ENRICHMENT_TRACKING env branch in Schema(d). When
// set, the single enrichment_errors table is skipped (dev-time split
// to generate 000008 cleanly); when unset, all 39 tables are present
// (the prod path). 39 - 1 = 38.
func TestSchemaCoverage_EnrichmentTrackingSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_ENRICHMENT_TRACKING", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 38 {
		t.Fatalf("Schema(postgres) with skip enrichment tables = %d, want 38 (39 - 1 enrichment_errors)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		if tbl.Name == "enrichment_errors" {
			t.Errorf("enrichment_errors table should be skipped when ATLAS_SCHEMA_SKIP_ENRICHMENT_TRACKING is set")
		}
	}
}

// TestSchemaCoverage_SeriesImagesSkipFlag covers the
// ATLAS_SCHEMA_SKIP_SERIES_IMAGES env branch in Schema(d). When set,
// the series_images table is skipped (dev-time split to generate 000009
// cleanly); when unset, all 39 tables are present (the prod path).
// 39 - 1 = 38.
func TestSchemaCoverage_SeriesImagesSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_SERIES_IMAGES", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 38 {
		t.Fatalf("Schema(postgres) with skip series_images tables = %d, want 38 (39 - 1 series_images)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		if tbl.Name == "series_images" {
			t.Errorf("series_images table should be skipped when ATLAS_SCHEMA_SKIP_SERIES_IMAGES is set")
		}
	}
}

// TestSchemaCoverage_AdminSkipFlag covers the ATLAS_SCHEMA_SKIP_ADMIN
// env branch in Schema(d). When set, the 5 admin tables
// (sonarr_instance, instance_secret, app_secret,
// external_service_config, external_service_quota_state) are skipped.
// addAuth and addGrab both depend on sonarr_instance (FK target), so
// ATLAS_SCHEMA_SKIP_ADMIN implies they must also be skipped — we set
// ATLAS_SCHEMA_SKIP_AUTH and ATLAS_SCHEMA_SKIP_GRAB alongside. With
// all three set: 39 - 5 admin - 2 auth - 3 grab = 29.
func TestSchemaCoverage_AdminSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_ADMIN", "1")
	t.Setenv("ATLAS_SCHEMA_SKIP_AUTH", "1")
	t.Setenv("ATLAS_SCHEMA_SKIP_GRAB", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 29 {
		t.Fatalf("Schema(postgres) with skip admin+auth+grab tables = %d, want 29 (39 - 5 admin - 2 auth - 3 grab)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "sonarr_instance", "instance_secret", "app_secret",
			"external_service_config", "external_service_quota_state":
			t.Errorf("admin table %q should be skipped when ATLAS_SCHEMA_SKIP_ADMIN is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_AuthSkipFlag covers the ATLAS_SCHEMA_SKIP_AUTH
// env branch in Schema(d). When set, the 2 auth tables (users,
// user_instance_tags) are skipped; when unset, all 39 tables are
// present. 39 - 2 = 37.
func TestSchemaCoverage_AuthSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_AUTH", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 37 {
		t.Fatalf("Schema(postgres) with skip auth tables = %d, want 37 (39 - 2 auth)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "users", "user_instance_tags":
			t.Errorf("auth table %q should be skipped when ATLAS_SCHEMA_SKIP_AUTH is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_GrabSkipFlag covers the ATLAS_SCHEMA_SKIP_GRAB env
// branch in Schema(d). When set, the 3 grab tables (grab_records,
// episode_grabs, download_links) are skipped; when unset, all 39 tables
// are present. 39 - 3 = 36.
func TestSchemaCoverage_GrabSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_GRAB", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 36 {
		t.Fatalf("Schema(postgres) with skip grab tables = %d, want 36 (39 - 3 grab)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "grab_records", "episode_grabs", "download_links":
			t.Errorf("grab table %q should be skipped when ATLAS_SCHEMA_SKIP_GRAB is set", tbl.Name)
		}
	}
}
