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
// count is 52 after D-7 story 468c: 51 prior (D-6) + 1 (media_assets).
func TestSchemaCoverage_BothDialects(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			if s == nil {
				t.Fatalf("Schema(%q) returned nil", d)
			}
			if len(s.Tables) != 52 {
				t.Fatalf("Schema(%q) tables = %d, want 52 (after D-7 media_assets)", d, len(s.Tables))
			}
		})
	}
}

// TestSchemaCoverage_TaxonomySkipFlag covers the ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS
// env branch in Schema(d). When set, the 4 join tables are skipped (used
// at dev-time to split the 000003_taxonomy migration from 000004_taxonomy_joins);
// when unset, all 42 tables are present (the prod path). 42 - 4 = 38.
func TestSchemaCoverage_TaxonomySkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 48 {
		t.Fatalf("Schema(postgres) with skip flag tables = %d, want 48 (52 - 4 joins)", len(s.Tables))
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
// pre-existing migrations); when unset, all 42 tables are present (the
// prod path). 42 default - 3 (skipped people) = 39.
func TestSchemaCoverage_PeopleSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_PEOPLE", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 49 {
		t.Fatalf("Schema(postgres) with skip people tables = %d, want 49 (52 - 3 people)", len(s.Tables))
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
// unset, all 42 tables are present (the prod path). 42 - 4 = 38.
func TestSchemaCoverage_SeriesExtrasSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_SERIES_EXTRAS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 48 {
		t.Fatalf("Schema(postgres) with skip series_extras tables = %d, want 48 (52 - 4 extras)", len(s.Tables))
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
	if len(s.Tables) != 52 {
		t.Fatalf("Load() tables = %d, want 52 (after D-7 media_assets)", len(s.Tables))
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
// 000007 cleanly); when unset, all 42 tables are present (the prod
// path). 42 - 3 = 39.
func TestSchemaCoverage_InstanceProjectionsSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_INSTANCE_PROJECTIONS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 49 {
		t.Fatalf("Schema(postgres) with skip projections tables = %d, want 49 (52 - 3 projections)", len(s.Tables))
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
// to generate 000008 cleanly); when unset, all 42 tables are present
// (the prod path). 42 - 1 = 41.
func TestSchemaCoverage_EnrichmentTrackingSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_ENRICHMENT_TRACKING", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 51 {
		t.Fatalf("Schema(postgres) with skip enrichment tables = %d, want 51 (52 - 1 enrichment_errors)", len(s.Tables))
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
// cleanly); when unset, all 42 tables are present (the prod path).
// 42 - 1 = 41.
func TestSchemaCoverage_SeriesImagesSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_SERIES_IMAGES", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 51 {
		t.Fatalf("Schema(postgres) with skip series_images tables = %d, want 51 (52 - 1 series_images)", len(s.Tables))
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
// addAuth, addGrab, addWatchdog, and addGrabAudit (D-6) all depend on
// sonarr_instance (FK target), so ATLAS_SCHEMA_SKIP_ADMIN implies they
// must also be skipped — we set ATLAS_SCHEMA_SKIP_AUTH,
// ATLAS_SCHEMA_SKIP_GRAB, ATLAS_SCHEMA_SKIP_WATCHDOG, and
// ATLAS_SCHEMA_SKIP_GRAB_AUDIT alongside. With all five set:
// 47 - 5 admin - 2 auth - 2 app_config - 3 grab - 2 watchdog
// - 3 grab_audit = 30. scan_runs stays in the schema because
// ATLAS_SCHEMA_SKIP_SCAN_RUNS is NOT set; the FK from grab_records is
// gone because grab tables are skipped.
func TestSchemaCoverage_AdminSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_ADMIN", "1")
	t.Setenv("ATLAS_SCHEMA_SKIP_AUTH", "1")
	t.Setenv("ATLAS_SCHEMA_SKIP_APP_CONFIG", "1")
	t.Setenv("ATLAS_SCHEMA_SKIP_GRAB", "1")
	t.Setenv("ATLAS_SCHEMA_SKIP_WATCHDOG", "1")
	t.Setenv("ATLAS_SCHEMA_SKIP_GRAB_AUDIT", "1")
	s := Schema(DialectPostgres)
	// 47 - 5 admin - 2 auth - 2 app_config - 3 grab - 2 watchdog
	// - 3 grab_audit = 30. addAppConfig + addGrabAudit depend on
	// sonarr_instance (FK target) so ATLAS_SCHEMA_SKIP_ADMIN implies
	// they must also be skipped.
	if len(s.Tables) != 31 {
		t.Fatalf("Schema(postgres) with skip admin+auth+app_config+grab+watchdog+grab_audit tables = %d, want 31 (52 - 5 admin - 2 auth - 2 app_config - 3 grab - 2 watchdog - 3 grab_audit - 4 qbit_runtime)", len(s.Tables))
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
// user_instance_tags) are skipped; when unset, all 42 tables are
// present. 42 - 2 = 40.
func TestSchemaCoverage_AuthSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_AUTH", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 50 {
		t.Fatalf("Schema(postgres) with skip auth tables = %d, want 50 (52 - 2 auth)", len(s.Tables))
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
// episode_grabs, download_links) are skipped; when unset, all 42 tables
// are present. 42 - 3 = 39.
func TestSchemaCoverage_GrabSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_GRAB", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 49 {
		t.Fatalf("Schema(postgres) with skip grab tables = %d, want 49 (52 - 3 grab)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "grab_records", "episode_grabs", "download_links":
			t.Errorf("grab table %q should be skipped when ATLAS_SCHEMA_SKIP_GRAB is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_WatchdogSkipFlag covers the
// ATLAS_SCHEMA_SKIP_WATCHDOG env branch in Schema(d). When set, the 2
// watchdog tables are skipped (used at dev-time to split the
// 000013_watchdog migration from earlier ones); when unset, all 42
// tables are present (the prod path). 42 - 2 = 40.
func TestSchemaCoverage_WatchdogSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_WATCHDOG", "1")
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		s := Schema(d)
		if len(s.Tables) != 50 {
			t.Fatalf("with skip set: Schema(%q) tables = %d, want 50 (52 - 2 watchdog)", d, len(s.Tables))
		}
		for _, tbl := range s.Tables {
			if tbl.Name == "watchdog_state" || tbl.Name == "watchdog_blacklist" {
				t.Errorf("watchdog table %q should be skipped when ATLAS_SCHEMA_SKIP_WATCHDOG is set", tbl.Name)
			}
		}
	}
}

// TestSchemaCoverage_ScanRunsSkipFlag covers the
// ATLAS_SCHEMA_SKIP_SCAN_RUNS env branch in Schema(d). When set, the
// scan_runs table is skipped (used at dev-time to generate D-4 story
// 465b migration 000015 cleanly without the table existing in prior
// migrations); when unset, all 42 tables are present (the prod path).
// 42 - 1 = 41. The grab_records.scan_run_id FK is ALSO skipped because
// addGrab's conditional finds no scan_runs table in s.
// TestSchemaCoverage_GrabAuditSkipFlag covers the
// ATLAS_SCHEMA_SKIP_GRAB_AUDIT env branch in Schema(d). When set, the
// 3 D-6 audit tables (decisions, cooldowns, origin_releases) are
// skipped; when unset, all 47 tables are present (the prod path).
// 47 - 3 = 44.
func TestSchemaCoverage_GrabAuditSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_GRAB_AUDIT", "1")
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		s := Schema(d)
		if len(s.Tables) != 49 {
			t.Fatalf("with skip set: Schema(%q) tables = %d, want 49 (52 - 3 grab_audit)", d, len(s.Tables))
		}
		for _, tbl := range s.Tables {
			switch tbl.Name {
			case "decisions", "cooldowns", "origin_releases":
				t.Errorf("grab_audit table %q should be skipped when ATLAS_SCHEMA_SKIP_GRAB_AUDIT is set", tbl.Name)
			}
		}
	}
}

func TestSchemaCoverage_ScanRunsSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_SCAN_RUNS", "1")
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		s := Schema(d)
		if len(s.Tables) != 51 {
			t.Fatalf("with skip set: Schema(%q) tables = %d, want 51 (52 - 1 scan_runs)", d, len(s.Tables))
		}
		for _, tbl := range s.Tables {
			if tbl.Name == "scan_runs" {
				t.Errorf("scan_runs table should be skipped when ATLAS_SCHEMA_SKIP_SCAN_RUNS is set")
			}
			if tbl.Name == "grab_records" {
				for _, fk := range tbl.ForeignKeys {
					if fk.Symbol == "grab_records_scan_run_id_fkey" {
						t.Errorf("grab_records.scan_run_id FK should NOT emit when scan_runs is skipped")
					}
				}
			}
		}
	}
}

// TestSchema_ScanRuns_Shape verifies the scan_runs table matches the
// ScanRunModel GORM contract: 15 columns, text(36) PK, 3 indexes, no
// FK, no CHECK. Asserts on both dialects (column types vary —
// timestamptz vs datetime — but counts/names stay stable).
func TestSchema_ScanRuns_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "scan_runs")

			if got, want := len(tbl.Columns), 15; got != want {
				t.Fatalf("scan_runs columns = %d, want %d", got, want)
			}

			wantCols := []string{
				"id", "instance_name", "trigger",
				"started_at", "finished_at", "status",
				"series_scanned", "candidates_found",
				"grabs_performed", "grabs_failed",
				"errors_count", "error_message", "dry_run",
				"created_at", "updated_at",
			}
			gotCols := make(map[string]bool, len(tbl.Columns))
			for _, c := range tbl.Columns {
				gotCols[c.Name] = true
			}
			for _, want := range wantCols {
				if !gotCols[want] {
					t.Errorf("missing column %q on scan_runs", want)
				}
			}

			if tbl.PrimaryKey == nil {
				t.Fatal("scan_runs missing primary key")
			}
			if len(tbl.PrimaryKey.Parts) != 1 ||
				tbl.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("scan_runs PK = %+v, want single col id", tbl.PrimaryKey.Parts)
			}

			if got, want := len(tbl.Indexes), 3; got != want {
				t.Errorf("scan_runs indexes = %d, want %d", got, want)
			}
			wantIdx := map[string]bool{
				"idx_scan_runs_created_at_id": false,
				"idx_scan_runs_started_at_id": false,
				"idx_scan_runs_instance_name": false,
			}
			for _, idx := range tbl.Indexes {
				if _, ok := wantIdx[idx.Name]; ok {
					wantIdx[idx.Name] = true
				}
			}
			for name, seen := range wantIdx {
				if !seen {
					t.Errorf("missing index %q on scan_runs", name)
				}
			}

			if len(tbl.ForeignKeys) != 0 {
				t.Errorf("scan_runs has %d FKs, want 0", len(tbl.ForeignKeys))
			}
		})
	}
}

// TestSchema_GrabRecords_ScanRunFKDropped — 467a / D-6 dropped the
// grab_records_scan_run_id_fkey FK to scan_runs(id) for the same
// reasoning that keeps decisions.scan_run_id unconstrained: scan_run_id
// is best-effort audit metadata; the rows outlive individual scan runs
// and watchdog replay rows legitimately reference no parent scan_run.
// This test guards against accidentally re-adding the FK.
func TestSchema_GrabRecords_ScanRunFKDropped(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			grab := mustTable(s, "grab_records")

			for _, fk := range grab.ForeignKeys {
				if fk.Symbol == "grab_records_scan_run_id_fkey" {
					t.Errorf("grab_records_scan_run_id_fkey FK MUST NOT be emitted on %q after 467a / D-6 drop", d)
				}
			}
		})
	}
}
