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

	atlasschema "ariga.io/atlas/sql/schema"
)

// TestSchemaCoverage_BothDialects walks Schema(d) for every shipped
// dialect. Touches every builder + helper transitively. Total table
// count is 60 after ADR-0015 Ф3 C1 followed_series (was 59 after E1
// webhook_inbox, 57 after W2-1 tmdb_changes_state, was 56 after 1083
// people_texts, was 55 after S-D drop of
// networks_i18n + production_companies_i18n): 52 prior (D-7 media_assets) +
// 1 (N-2a discovery_lists) + 1 (B3a season_texts) + 1 (584a
// series_media_texts) + 1 (S-C2 season_media_texts) + 1 (S-G
// person_credits_texts) + 1 (1083 people_texts), always-on i18n text
// tables alongside series_texts/episode_texts/season_texts.
func TestSchemaCoverage_BothDialects(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			if s == nil {
				t.Fatalf("Schema(%q) returned nil", d)
			}
			if len(s.Tables) != 78 {
				t.Fatalf("Schema(%q) tables = %d, want 78 (after Ф8-U-2 requests)", d, len(s.Tables))
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
	if len(s.Tables) != 74 {
		t.Fatalf("Schema(postgres) with skip flag tables = %d, want 74 (78 - 4 joins)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "series_genres", "series_networks", "series_companies", "series_keywords":
			t.Errorf("join table %q should be skipped when ATLAS_SCHEMA_SKIP_TAXONOMY_JOINS is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_PeopleSkipFlag covers the ATLAS_SCHEMA_SKIP_PEOPLE
// env branch in Schema(d). When set, the 5 people-domain tables are
// skipped (used at dev-time to split the 000005_people migration from
// pre-existing migrations); when unset, all tables are present (the
// prod path). 57 default - 5 (skipped people) = 52.
func TestSchemaCoverage_PeopleSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_PEOPLE", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 73 {
		t.Fatalf("Schema(postgres) with skip people tables = %d, want 73 (78 - 5 people)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "people", "person_credits", "person_biographies", "person_credits_texts", "people_texts":
			t.Errorf("people-domain table %q should be skipped when ATLAS_SCHEMA_SKIP_PEOPLE is set", tbl.Name)
		}
	}
}

// TestSchemaCoverage_SeriesExtrasSkipFlag covers the
// ATLAS_SCHEMA_SKIP_SERIES_EXTRAS env branch in Schema(d). When set, the
// 5 series-extras tables (videos, content_ratings, external_ids,
// series_recommendations, discovery_lists) are skipped (used at
// dev-time to split the 000006_series_extras / 000021_discovery_lists
// migrations from pre-existing migrations); when unset, all tables are
// present (the prod path). 72 - 5 = 67.
func TestSchemaCoverage_SeriesExtrasSkipFlag(t *testing.T) {
	t.Setenv("ATLAS_SCHEMA_SKIP_SERIES_EXTRAS", "1")
	s := Schema(DialectPostgres)
	if len(s.Tables) != 73 {
		t.Fatalf("Schema(postgres) with skip series_extras tables = %d, want 73 (78 - 5 extras)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "videos", "content_ratings", "external_ids", "series_recommendations", "discovery_lists":
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
	if len(s.Tables) != 78 {
		t.Fatalf("Load() tables = %d, want 78 (after Ф8-U-2 requests)", len(s.Tables))
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
	if len(s.Tables) != 75 {
		t.Fatalf("Schema(postgres) with skip projections tables = %d, want 75 (78 - 3 projections)", len(s.Tables))
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
	if len(s.Tables) != 77 {
		t.Fatalf("Schema(postgres) with skip enrichment tables = %d, want 77 (78 - 1 enrichment_errors)", len(s.Tables))
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
	if len(s.Tables) != 77 {
		t.Fatalf("Schema(postgres) with skip series_images tables = %d, want 77 (78 - 1 series_images)", len(s.Tables))
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
	if len(s.Tables) != 48 {
		t.Fatalf("Schema(postgres) with skip admin+auth+app_config+grab+watchdog+grab_audit tables = %d, want 48 (78 - 4 auth incl. requests - 5 admin - 3 app_config - 3 grab - 2 watchdog - 3 grab_audit - 4 qbit_runtime - 5 followed_series/notification_agents/discovery_blocklist/notification_outbox/notified_events Ф8-U-5+U-5c)", len(s.Tables))
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
	if len(s.Tables) != 69 {
		t.Fatalf("Schema(postgres) with skip auth tables = %d, want 69 (78 - 4 auth incl. requests - 5 followed_series/notification_agents/discovery_blocklist/notification_outbox/notified_events Ф8-U-5+U-5c)", len(s.Tables))
	}
	for _, tbl := range s.Tables {
		switch tbl.Name {
		case "users", "user_instance_tags", "user_instance_access":
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
	if len(s.Tables) != 75 {
		t.Fatalf("Schema(postgres) with skip grab tables = %d, want 75 (78 - 3 grab)", len(s.Tables))
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
		if len(s.Tables) != 76 {
			t.Fatalf("with skip set: Schema(%q) tables = %d, want 76 (78 - 2 watchdog)", d, len(s.Tables))
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
		if len(s.Tables) != 75 {
			t.Fatalf("with skip set: Schema(%q) tables = %d, want 75 (78 - 3 grab_audit)", d, len(s.Tables))
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
		if len(s.Tables) != 77 {
			t.Fatalf("with skip set: Schema(%q) tables = %d, want 77 (78 - 1 scan_runs)", d, len(s.Tables))
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

// TestD2_DiscoveryListsTable_BothDialects verifies the discovery_lists
// table is present on both dialects with the correct column inventory.
// N-2a / story 502 — PRD §5.1.1.
func TestD2_DiscoveryListsTable_BothDialects(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "discovery_lists")

			if got, want := len(tbl.Columns), 8; got != want {
				t.Fatalf("discovery_lists columns = %d, want %d", got, want)
			}
			wantCols := []string{"kind", "param", "language", "series_id", "position", "refreshed_at", "year", "tmdb_rating"}
			gotCols := make(map[string]bool, len(tbl.Columns))
			for _, c := range tbl.Columns {
				gotCols[c.Name] = true
			}
			for _, want := range wantCols {
				if !gotCols[want] {
					t.Errorf("missing column %q on discovery_lists", want)
				}
			}
		})
	}
}

// TestD2_DiscoveryListsCompositePK pins the 4-column composite PK order
// (kind, param, language, series_id). Order is part of the contract —
// readers binary-search by (kind, param, language) prefix.
func TestD2_DiscoveryListsCompositePK(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "discovery_lists")

			if tbl.PrimaryKey == nil {
				t.Fatal("discovery_lists missing primary key")
			}
			if got, want := len(tbl.PrimaryKey.Parts), 4; got != want {
				t.Fatalf("discovery_lists PK parts = %d, want %d", got, want)
			}
			wantOrder := []string{"kind", "param", "language", "series_id"}
			for i, want := range wantOrder {
				got := tbl.PrimaryKey.Parts[i].C.Name
				if got != want {
					t.Errorf("discovery_lists PK[%d] = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// TestD2_DiscoveryListsLookupIndex pins the discovery_lists_lookup_idx
// shape (kind, param, language, position) non-unique. Primary read path
// for /discovery/trending|popular|by_* handlers.
func TestD2_DiscoveryListsLookupIndex(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "discovery_lists")

			var idx *struct {
				cols   []string
				unique bool
			}
			for _, ix := range tbl.Indexes {
				if ix.Name != "discovery_lists_lookup_idx" {
					continue
				}
				cols := make([]string, 0, len(ix.Parts))
				for _, p := range ix.Parts {
					if p.C != nil {
						cols = append(cols, p.C.Name)
					}
				}
				idx = &struct {
					cols   []string
					unique bool
				}{cols: cols, unique: ix.Unique}
				break
			}
			if idx == nil {
				t.Fatal("discovery_lists_lookup_idx not found")
			}
			if idx.unique {
				t.Errorf("discovery_lists_lookup_idx unique = true, want non-unique")
			}
			wantCols := []string{"kind", "param", "language", "position"}
			if len(idx.cols) != len(wantCols) {
				t.Fatalf("discovery_lists_lookup_idx cols = %v, want %v", idx.cols, wantCols)
			}
			for i, want := range wantCols {
				if idx.cols[i] != want {
					t.Errorf("discovery_lists_lookup_idx[%d] = %q, want %q", i, idx.cols[i], want)
				}
			}
		})
	}
}

// TestD2_DiscoveryListsRefreshIndex pins the discovery_lists_refresh_idx
// shape (kind, refreshed_at) non-unique. Used by DiscoveryWorker
// staleness sweep ("which list-kinds are due for refresh?").
func TestD2_DiscoveryListsRefreshIndex(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "discovery_lists")

			var found bool
			for _, ix := range tbl.Indexes {
				if ix.Name != "discovery_lists_refresh_idx" {
					continue
				}
				found = true
				if ix.Unique {
					t.Errorf("discovery_lists_refresh_idx unique = true, want non-unique")
				}
				wantCols := []string{"kind", "refreshed_at"}
				if got := len(ix.Parts); got != len(wantCols) {
					t.Fatalf("discovery_lists_refresh_idx parts = %d, want %d", got, len(wantCols))
				}
				for i, want := range wantCols {
					got := ix.Parts[i].C.Name
					if got != want {
						t.Errorf("discovery_lists_refresh_idx[%d] = %q, want %q", i, got, want)
					}
				}
			}
			if !found {
				t.Fatal("discovery_lists_refresh_idx not found")
			}
		})
	}
}

// TestD2_DiscoveryListsSeriesFKCascade pins the FK direction +
// ON DELETE CASCADE invariant. Sibling test to the series_recommendations
// FK check — same family of "dead-without-parent" projection rows.
func TestD2_DiscoveryListsSeriesFKCascade(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "discovery_lists")

			if got, want := len(tbl.ForeignKeys), 1; got != want {
				t.Fatalf("discovery_lists FKs = %d, want %d", got, want)
			}
			fk := tbl.ForeignKeys[0]
			if fk.Symbol != "discovery_lists_series_id_fkey" {
				t.Errorf("FK name = %q, want discovery_lists_series_id_fkey", fk.Symbol)
			}
			if fk.RefTable == nil || fk.RefTable.Name != "series" {
				t.Errorf("FK RefTable = %v, want series", fk.RefTable)
			}
			if fk.OnDelete != atlasschema.Cascade {
				t.Errorf("FK OnDelete = %q, want CASCADE", fk.OnDelete)
			}
			if got := len(fk.Columns); got != 1 || fk.Columns[0].Name != "series_id" {
				t.Errorf("FK Columns = %v, want [series_id]", fk.Columns)
			}
		})
	}
}

func TestSchema_NotificationTables_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)

			outbox := mustTable(s, "notification_outbox")
			if got, want := len(outbox.Columns), 9; got != want {
				t.Fatalf("notification_outbox columns = %d, want %d", got, want)
			}
			wantOutbox := []string{
				"id", "user_id", "event_type", "payload", "status",
				"attempts", "next_attempt_at", "dedup_key", "created_at",
			}
			gotOutbox := make(map[string]bool, len(outbox.Columns))
			for _, c := range outbox.Columns {
				gotOutbox[c.Name] = true
			}
			for _, want := range wantOutbox {
				if !gotOutbox[want] {
					t.Errorf("missing column %q on notification_outbox", want)
				}
			}
			if outbox.PrimaryKey == nil || len(outbox.PrimaryKey.Parts) != 1 ||
				outbox.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("notification_outbox PK = %+v, want single col id", outbox.PrimaryKey)
			}
			if got, want := len(outbox.Indexes), 3; got != want {
				t.Errorf("notification_outbox indexes = %d, want %d", got, want)
			}
			wantIdx := map[string]bool{
				"notification_outbox_pending":      false,
				"notification_outbox_dedup":        false,
				"notification_outbox_user_pending": false,
			}
			for _, idx := range outbox.Indexes {
				if _, ok := wantIdx[idx.Name]; ok {
					wantIdx[idx.Name] = true
				}
			}
			for name, seen := range wantIdx {
				if !seen {
					t.Errorf("missing index %q on notification_outbox", name)
				}
			}
			// Ф8-U-5c: user_id FK → users CASCADE (target follower).
			if len(outbox.ForeignKeys) != 1 {
				t.Errorf("notification_outbox has %d FKs, want 1", len(outbox.ForeignKeys))
			} else if outbox.ForeignKeys[0].Symbol != "notification_outbox_user_id_fkey" {
				t.Errorf("notification_outbox FK = %q, want notification_outbox_user_id_fkey", outbox.ForeignKeys[0].Symbol)
			}

			// Ф8-U-5c: notified_events per-user dedup — 4 cols, PK
			// (user_id, event_type, entity_key), one user_id FK → users.
			ne := mustTable(s, "notified_events")
			if got, want := len(ne.Columns), 4; got != want {
				t.Fatalf("notified_events columns = %d, want %d", got, want)
			}
			wantNE := []string{"user_id", "event_type", "entity_key", "first_seen_at"}
			gotNE := make(map[string]bool, len(ne.Columns))
			for _, c := range ne.Columns {
				gotNE[c.Name] = true
			}
			for _, want := range wantNE {
				if !gotNE[want] {
					t.Errorf("missing column %q on notified_events", want)
				}
			}
			if ne.PrimaryKey == nil || len(ne.PrimaryKey.Parts) != 3 {
				t.Fatalf("notified_events PK = %+v, want 3-col composite", ne.PrimaryKey)
			}
			wantPK := map[string]bool{"user_id": false, "event_type": false, "entity_key": false}
			for _, p := range ne.PrimaryKey.Parts {
				if _, ok := wantPK[p.C.Name]; ok {
					wantPK[p.C.Name] = true
				}
			}
			for name, seen := range wantPK {
				if !seen {
					t.Errorf("notified_events PK missing part %q", name)
				}
			}
			if len(ne.ForeignKeys) != 1 {
				t.Errorf("notified_events has %d FKs, want 1", len(ne.ForeignKeys))
			} else if ne.ForeignKeys[0].Symbol != "notified_events_user_id_fkey" {
				t.Errorf("notified_events FK = %q, want notified_events_user_id_fkey", ne.ForeignKeys[0].Symbol)
			}

			agents := mustTable(s, "notification_agents")
			if got, want := len(agents.Columns), 7; got != want {
				t.Fatalf("notification_agents columns = %d, want %d", got, want)
			}
			wantAgents := []string{"id", "user_id", "name", "enabled", "config_encrypted", "event_types", "created_at"}
			gotAgents := make(map[string]bool, len(agents.Columns))
			for _, c := range agents.Columns {
				gotAgents[c.Name] = true
			}
			for _, want := range wantAgents {
				if !gotAgents[want] {
					t.Errorf("missing column %q on notification_agents", want)
				}
			}
			if agents.PrimaryKey == nil || len(agents.PrimaryKey.Parts) != 1 ||
				agents.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("notification_agents PK = %+v, want single col id", agents.PrimaryKey)
			}
			// Ф8-U-5: user_id FK → users CASCADE (owner).
			if len(agents.ForeignKeys) != 1 {
				t.Errorf("notification_agents has %d FKs, want 1", len(agents.ForeignKeys))
			} else if agents.ForeignKeys[0].Symbol != "notification_agents_user_id_fkey" {
				t.Errorf("notification_agents FK = %q, want notification_agents_user_id_fkey", agents.ForeignKeys[0].Symbol)
			}
		})
	}
}

// TestSchema_DiscoveryRows_Shape verifies discovery_rows on both dialects:
// 9 cols, surrogate PK id, one index on position, no FK.
func TestSchema_DiscoveryRows_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "discovery_rows")
			if got, want := len(tbl.Columns), 9; got != want {
				t.Fatalf("discovery_rows columns = %d, want %d", got, want)
			}
			want := []string{"id", "row_type", "source", "media_type",
				"params", "position", "enabled", "title", "created_at"}
			got := make(map[string]bool, len(tbl.Columns))
			for _, c := range tbl.Columns {
				got[c.Name] = true
			}
			for _, w := range want {
				if !got[w] {
					t.Errorf("missing column %q", w)
				}
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 ||
				tbl.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("discovery_rows PK = %+v, want single col id", tbl.PrimaryKey)
			}
			if len(tbl.ForeignKeys) != 0 {
				t.Errorf("discovery_rows has %d FKs, want 0", len(tbl.ForeignKeys))
			}
			var idx bool
			for _, ix := range tbl.Indexes {
				if ix.Name == "discovery_rows_position_idx" {
					idx = true
				}
			}
			if !idx {
				t.Error("missing discovery_rows_position_idx")
			}
		})
	}
}

// TestSchema_FollowedSeries_Shape verifies followed_series on both dialects:
// 3 cols, composite PK (user_id, series_id), two CASCADE FKs (series, users).
// ADR-0015 Ф3 C1 + Ф8-U-5 per-user.
func TestSchema_FollowedSeries_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "followed_series")
			if got, want := len(tbl.Columns), 3; got != want {
				t.Fatalf("followed_series columns = %d, want %d", got, want)
			}
			want := []string{"user_id", "series_id", "created_at"}
			got := make(map[string]bool, len(tbl.Columns))
			for _, c := range tbl.Columns {
				got[c.Name] = true
			}
			for _, w := range want {
				if !got[w] {
					t.Errorf("missing column %q", w)
				}
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 ||
				tbl.PrimaryKey.Parts[0].C.Name != "user_id" || tbl.PrimaryKey.Parts[1].C.Name != "series_id" {
				t.Errorf("followed_series PK = %+v, want composite (user_id, series_id)", tbl.PrimaryKey)
			}
			if len(tbl.ForeignKeys) != 2 {
				t.Errorf("followed_series has %d FKs, want 2 (series + users)", len(tbl.ForeignKeys))
			}
			names := map[string]bool{}
			for _, fk := range tbl.ForeignKeys {
				names[fk.Symbol] = true
			}
			for _, want := range []string{"followed_series_series_id_fkey", "followed_series_user_id_fkey"} {
				if !names[want] {
					t.Errorf("missing FK %q on followed_series", want)
				}
			}
		})
	}
}

// TestSchema_DiscoveryBlocklist_Shape verifies discovery_blocklist on both
// dialects: 6 cols, surrogate PK id, one UNIQUE index on (user_id, kind,
// ref_id), user_id FK → users. ADR-0017 Ф5 S3 + Ф8-U-5 per-user.
func TestSchema_DiscoveryBlocklist_Shape(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			tbl := mustTable(s, "discovery_blocklist")
			if got, want := len(tbl.Columns), 6; got != want {
				t.Fatalf("discovery_blocklist columns = %d, want %d", got, want)
			}
			want := []string{"id", "user_id", "kind", "ref_id", "label", "created_at"}
			got := make(map[string]bool, len(tbl.Columns))
			for _, c := range tbl.Columns {
				got[c.Name] = true
			}
			for _, w := range want {
				if !got[w] {
					t.Errorf("missing column %q", w)
				}
			}
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 ||
				tbl.PrimaryKey.Parts[0].C.Name != "id" {
				t.Errorf("discovery_blocklist PK = %+v, want single col id", tbl.PrimaryKey)
			}
			// Ф8-U-5: user_id FK → users CASCADE (owner).
			if len(tbl.ForeignKeys) != 1 {
				t.Errorf("discovery_blocklist has %d FKs, want 1", len(tbl.ForeignKeys))
			} else if tbl.ForeignKeys[0].Symbol != "discovery_blocklist_user_id_fkey" {
				t.Errorf("discovery_blocklist FK = %q, want discovery_blocklist_user_id_fkey", tbl.ForeignKeys[0].Symbol)
			}
			var uniq bool
			for _, ix := range tbl.Indexes {
				if ix.Name == "discovery_blocklist_kind_ref" {
					uniq = ix.Unique
					if len(ix.Parts) != 3 ||
						ix.Parts[0].C.Name != "user_id" || ix.Parts[1].C.Name != "kind" || ix.Parts[2].C.Name != "ref_id" {
						t.Errorf("discovery_blocklist_kind_ref parts = %v, want [user_id kind ref_id]", ix.Parts)
					}
				}
			}
			if !uniq {
				t.Error("missing UNIQUE index discovery_blocklist_kind_ref")
			}
		})
	}
}
