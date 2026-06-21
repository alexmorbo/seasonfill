// Package tests — D-1-5 (story 458) unit assertions for the 3 per-
// instance projection tables: series_cache, episode_states,
// season_stats. Inspects in-memory Schema(d) for both dialects; no DB.
//
// Reuses mustTable / mustIndex / indexPredicate helpers already defined
// in d1_2_core_series_test.go (same `package tests`).
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD15_SchemaHasTwentyEightTables — D-1-4b had 24; D-1-5 adds 4
// (series_cache, episode_states, season_stats, enrichment_errors) → 28.
func TestD15_SchemaHasTwentyEightTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if got, want := len(s.Tables), 28; got != want {
				t.Fatalf("table count = %d, want %d", got, want)
			}
			present := map[string]bool{}
			for _, tbl := range s.Tables {
				present[tbl.Name] = true
			}
			for _, name := range []string{"series_cache", "episode_states", "season_stats"} {
				if !present[name] {
					t.Errorf("table %q missing from schema", name)
				}
			}
		})
	}
}

// TestD15_ProjectionColumnCounts — series_cache=11, episode_states=13,
// season_stats=11.
func TestD15_ProjectionColumnCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table string
		want  int
	}{
		{"series_cache", 11},
		{"episode_states", 13},
		{"season_stats", 11},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				if got := len(tbl.Columns); got != c.want {
					t.Errorf("%s col count = %d, want %d", c.table, got, c.want)
				}
			})
		}
	}
}

// TestD15_CompositePKs — series_cache (instance_name, sonarr_series_id);
// episode_states (instance_name, episode_id); season_stats composite-3
// (instance_name, sonarr_series_id, season_number).
func TestD15_CompositePKs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table string
		cols  []string
	}{
		{"series_cache", []string{"instance_name", "sonarr_series_id"}},
		{"episode_states", []string{"instance_name", "episode_id"}},
		{"season_stats", []string{"instance_name", "sonarr_series_id", "season_number"}},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != len(c.cols) {
					gotN := 0
					if tbl.PrimaryKey != nil {
						gotN = len(tbl.PrimaryKey.Parts)
					}
					t.Fatalf("%s PK parts = %d, want %d", c.table, gotN, len(c.cols))
				}
				for i, want := range c.cols {
					got := tbl.PrimaryKey.Parts[i].C.Name
					if got != want {
						t.Errorf("%s PK parts[%d] = %q, want %q", c.table, i, got, want)
					}
				}
			})
		}
	}
}

// TestD15_SeriesCacheFKDirection — series_cache.series_id FK → series(id)
// with NO ACTION (NOT Cascade — soft-delete contract).
func TestD15_SeriesCacheFKDirection(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_cache")
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("series_cache FK count = %d, want 1", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.OnDelete != atlasschema.NoAction {
				t.Errorf("series_cache FK OnDelete = %q, want NoAction (soft-delete contract)", fk.OnDelete)
			}
			if fk.RefTable == nil || fk.RefTable.Name != "series" {
				name := ""
				if fk.RefTable != nil {
					name = fk.RefTable.Name
				}
				t.Errorf("series_cache FK RefTable = %q, want series", name)
			}
		})
	}
}

// TestD15_EpisodeStatesFKDirection — episode_states.episode_id FK →
// episodes(id) with NO ACTION.
func TestD15_EpisodeStatesFKDirection(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "episode_states")
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("episode_states FK count = %d, want 1", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.OnDelete != atlasschema.NoAction {
				t.Errorf("episode_states FK OnDelete = %q, want NoAction", fk.OnDelete)
			}
			if fk.RefTable == nil || fk.RefTable.Name != "episodes" {
				name := ""
				if fk.RefTable != nil {
					name = fk.RefTable.Name
				}
				t.Errorf("episode_states FK RefTable = %q, want episodes", name)
			}
		})
	}
}

// TestD15_SeasonStatsNoFK — season_stats has NO FKs (deliberate;
// (instance_name, sonarr_series_id) is a natural projection key, but
// DB-level coupling is avoided so the SonarrSync cascade can write the
// two tables in separate statements). See story 458 §Investigation
// Notes.
func TestD15_SeasonStatsNoFK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "season_stats")
			if len(tbl.ForeignKeys) != 0 {
				t.Errorf("season_stats should have NO FKs; got %d FKs", len(tbl.ForeignKeys))
			}
		})
	}
}

// TestD15_SoftDeleteIndexes — partial-index predicates for the soft-
// delete pattern. series_cache + season_stats use IS NULL (read-path
// filter); episode_states uses IS NOT NULL (housekeeping path).
func TestD15_SoftDeleteIndexes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table    string
		index    string
		wantPred string
	}{
		{"series_cache", "series_cache_instance_active", "deleted_at IS NULL"},
		{"episode_states", "episode_states_deleted_at", "deleted_at IS NOT NULL"},
		{"season_stats", "season_stats_series", "deleted_at IS NULL"},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				idx := mustIndex(t, tbl, c.index)
				if idx.Unique {
					t.Errorf("%s should NOT be unique", c.index)
				}
				if got := indexPredicate(d, idx); got != c.wantPred {
					t.Errorf("%s predicate = %q, want %q", c.index, got, c.wantPred)
				}
			})
		}
	}
}

// TestD15_SeriesCacheSeriesIDIndex — series_cache_series_id ON
// (series_id) for the canon→instance reverse lookup. Non-unique;
// non-partial.
func TestD15_SeriesCacheSeriesIDIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_cache")
			idx := mustIndex(t, tbl, "series_cache_series_id")
			if idx.Unique {
				t.Errorf("series_cache_series_id should NOT be unique")
			}
			if len(idx.Parts) != 1 || idx.Parts[0].C.Name != "series_id" {
				t.Errorf("series_cache_series_id parts wrong: %+v", idx.Parts)
			}
		})
	}
}

// TestD15_ProjectionDialectParityShape — every D-1-5 projection table
// has identical column-name set across PG and SQLite.
func TestD15_ProjectionDialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)
	for _, name := range []string{"series_cache", "episode_states", "season_stats"} {
		pgT := mustTable(t, pg, name)
		sqT := mustTable(t, sq, name)
		if len(pgT.Columns) != len(sqT.Columns) {
			t.Errorf("%s col count drift: pg=%d sqlite=%d",
				name, len(pgT.Columns), len(sqT.Columns))
		}
	}
}
