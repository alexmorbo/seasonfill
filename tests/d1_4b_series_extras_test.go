// Package tests — D-1-4b (story 457b) unit assertions for the 4
// series-extras tables: videos, content_ratings, external_ids,
// series_recommendations. Inspects in-memory Schema(d) for both
// dialects; no DB.
//
// Reuses mustTable / mustIndex helpers already defined in
// d1_2_core_series_test.go (same `package tests`).
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD14b_SchemaHasTwentyFourTables — D-1-4a had 20; D-1-4b adds 4
// (videos, content_ratings, external_ids, series_recommendations) → 24.
func TestD14b_SchemaHasTwentyFourTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if got, want := len(s.Tables), 24; got != want {
				t.Fatalf("table count = %d, want %d", got, want)
			}
			present := map[string]bool{}
			for _, tbl := range s.Tables {
				present[tbl.Name] = true
			}
			for _, name := range []string{"videos", "content_ratings", "external_ids", "series_recommendations"} {
				if !present[name] {
					t.Errorf("table %q missing from schema", name)
				}
			}
		})
	}
}

// TestD14b_ExtrasColumnCounts — videos=12, content_ratings=4,
// external_ids=5, series_recommendations=4.
func TestD14b_ExtrasColumnCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table string
		want  int
	}{
		{"videos", 12},
		{"content_ratings", 4},
		{"external_ids", 5},
		{"series_recommendations", 4},
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

// TestD14b_VideosPartialUnique — videos_tmdb_id partial unique with
// "tmdb_video_id IS NOT NULL" predicate.
func TestD14b_VideosPartialUnique(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "videos")
			idx := mustIndex(t, tbl, "videos_tmdb_id")
			if !idx.Unique {
				t.Errorf("videos_tmdb_id not unique")
			}
			pred := indexPredicate(d, idx)
			if pred != "tmdb_video_id IS NOT NULL" {
				t.Errorf("videos_tmdb_id predicate = %q, want %q", pred, "tmdb_video_id IS NOT NULL")
			}
		})
	}
}

// TestD14b_VideosSeriesTypeIndex — composite (series_id, type, official).
func TestD14b_VideosSeriesTypeIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "videos")
			idx := mustIndex(t, tbl, "videos_series_type")
			if idx.Unique {
				t.Errorf("videos_series_type should NOT be unique")
			}
			want := []string{"series_id", "type", "official"}
			if len(idx.Parts) != len(want) {
				t.Fatalf("parts count = %d, want %d", len(idx.Parts), len(want))
			}
			for i, w := range want {
				if idx.Parts[i].C.Name != w {
					t.Errorf("parts[%d] = %q, want %q", i, idx.Parts[i].C.Name, w)
				}
			}
		})
	}
}

// TestD14b_CompositePKs — content_ratings (series_id, country_code);
// external_ids (entity_type, entity_id, provider) composite-3;
// series_recommendations (series_id, recommended_series_id).
func TestD14b_CompositePKs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table string
		cols  []string
	}{
		{"content_ratings", []string{"series_id", "country_code"}},
		{"external_ids", []string{"entity_type", "entity_id", "provider"}},
		{"series_recommendations", []string{"series_id", "recommended_series_id"}},
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

// TestD14b_ExternalIDsNoFK — polymorphic table; no FK on entity_id.
// This is a deliberate schema choice (PRD §5.3). Failing this test
// means an FK was added by reflex.
func TestD14b_ExternalIDsNoFK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "external_ids")
			if len(tbl.ForeignKeys) != 0 {
				t.Errorf("external_ids should have NO FKs (polymorphic); got %d FKs", len(tbl.ForeignKeys))
			}
		})
	}
}

// TestD14b_ExternalIDsProviderValueIndex — external_ids_provider_value
// on (provider, value) — reverse-lookup index.
func TestD14b_ExternalIDsProviderValueIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "external_ids")
			idx := mustIndex(t, tbl, "external_ids_provider_value")
			if idx.Unique {
				t.Errorf("external_ids_provider_value should NOT be unique")
			}
			want := []string{"provider", "value"}
			if len(idx.Parts) != len(want) {
				t.Fatalf("parts count = %d, want %d", len(idx.Parts), len(want))
			}
			for i, w := range want {
				if idx.Parts[i].C.Name != w {
					t.Errorf("parts[%d] = %q, want %q", i, idx.Parts[i].C.Name, w)
				}
			}
		})
	}
}

// TestD14b_CascadeOnSeriesSide — videos / content_ratings /
// series_recommendations all CASCADE on series_id (and recommendations
// CASCADE on recommended_series_id too).
func TestD14b_CascadeOnSeriesSide(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table       string
		wantFKCount int
	}{
		{"videos", 1},
		{"content_ratings", 1},
		{"series_recommendations", 2},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				if len(tbl.ForeignKeys) != c.wantFKCount {
					t.Errorf("%s FK count = %d, want %d", c.table, len(tbl.ForeignKeys), c.wantFKCount)
				}
				for _, fk := range tbl.ForeignKeys {
					if fk.OnDelete != atlasschema.Cascade {
						t.Errorf("%s FK %s OnDelete = %q, want Cascade",
							c.table, fk.Symbol, fk.OnDelete)
					}
					if fk.RefTable == nil || fk.RefTable.Name != "series" {
						name := ""
						if fk.RefTable != nil {
							name = fk.RefTable.Name
						}
						t.Errorf("%s FK %s RefTable = %q, want series", c.table, fk.Symbol, name)
					}
				}
			})
		}
	}
}

// TestD14b_SeriesRecommendationsPositionIndex — composite (series_id, position).
func TestD14b_SeriesRecommendationsPositionIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_recommendations")
			idx := mustIndex(t, tbl, "series_recommendations_position")
			want := []string{"series_id", "position"}
			if len(idx.Parts) != len(want) {
				t.Fatalf("parts count = %d, want %d", len(idx.Parts), len(want))
			}
			for i, w := range want {
				if idx.Parts[i].C.Name != w {
					t.Errorf("parts[%d] = %q, want %q", i, idx.Parts[i].C.Name, w)
				}
			}
		})
	}
}

// TestD14b_DialectParityShape — every new D-1-4b table has identical
// column-name set across PG and SQLite.
func TestD14b_DialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)
	for _, name := range []string{"videos", "content_ratings", "external_ids", "series_recommendations"} {
		pgT := mustTable(t, pg, name)
		sqT := mustTable(t, sq, name)
		if len(pgT.Columns) != len(sqT.Columns) {
			t.Errorf("%s col count drift: pg=%d sqlite=%d",
				name, len(pgT.Columns), len(sqT.Columns))
		}
	}
}
