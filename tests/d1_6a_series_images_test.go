// Package tests — D-1-6a (story 459a) unit assertions for the
// series_images table (000009 single-table migration). Inspects
// in-memory Schema(d) for both dialects; no DB.
//
// Reuses mustTable / mustIndex helpers already defined in
// d1_2_core_series_test.go (same `package tests`).
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD16a_SchemaHasTwentyNineTables — D-1-5 had 28; D-1-6a adds 1
// (series_images) → 29.
func TestD16a_SchemaHasTwentyNineTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if got, want := len(s.Tables), 29; got != want {
				t.Fatalf("table count = %d, want %d", got, want)
			}
			present := false
			for _, tbl := range s.Tables {
				if tbl.Name == "series_images" {
					present = true
					break
				}
			}
			if !present {
				t.Errorf("table series_images missing from schema")
			}
		})
	}
}

// TestD16a_SeriesImagesColumnCount — 13 columns (id, series_id,
// language, kind, tmdb_path, asset_hash, iso_lang, vote_average,
// vote_count, width, height, position, updated_at).
func TestD16a_SeriesImagesColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_images")
			if got, want := len(tbl.Columns), 13; got != want {
				t.Errorf("series_images col count = %d, want %d", got, want)
			}
		})
	}
}

// TestD16a_SeriesImagesSinglePK — single surrogate id PK (NOT a
// composite; matches videos/people pattern).
func TestD16a_SeriesImagesSinglePK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_images")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 {
				gotN := 0
				if tbl.PrimaryKey != nil {
					gotN = len(tbl.PrimaryKey.Parts)
				}
				t.Fatalf("series_images PK parts = %d, want 1", gotN)
			}
			if got := tbl.PrimaryKey.Parts[0].C.Name; got != "id" {
				t.Errorf("series_images PK col = %q, want id", got)
			}
		})
	}
}

// TestD16a_SeriesImagesFKDirection — series_images.series_id FK →
// series(id) with CASCADE (NOT NoAction — derived enrichment data
// dies with canon).
func TestD16a_SeriesImagesFKDirection(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_images")
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("series_images FK count = %d, want 1", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.OnDelete != atlasschema.Cascade {
				t.Errorf("series_images FK OnDelete = %q, want Cascade (derived enrichment data dies with canon)", fk.OnDelete)
			}
			if fk.RefTable == nil || fk.RefTable.Name != "series" {
				name := ""
				if fk.RefTable != nil {
					name = fk.RefTable.Name
				}
				t.Errorf("series_images FK RefTable = %q, want series", name)
			}
		})
	}
}

// TestD16a_SeriesImagesUniqueComposite — UNIQUE composite-4 index on
// (series_id, language, kind, position).
func TestD16a_SeriesImagesUniqueComposite(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_images")
			idx := mustIndex(t, tbl, "series_images_series_lang_kind_position")
			if !idx.Unique {
				t.Errorf("series_images_series_lang_kind_position should be UNIQUE")
			}
			want := []string{"series_id", "language", "kind", "position"}
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

// TestD16a_SeriesImagesReadPathIndex — non-unique composite-3 index on
// (series_id, kind, position) for the composer's hot read path.
func TestD16a_SeriesImagesReadPathIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_images")
			idx := mustIndex(t, tbl, "series_images_series_kind_position")
			if idx.Unique {
				t.Errorf("series_images_series_kind_position should NOT be unique")
			}
			want := []string{"series_id", "kind", "position"}
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

// TestD16a_SeriesImagesNullability — language + kind + tmdb_path +
// position + updated_at NOT NULL; asset_hash + iso_lang + vote_average
// + vote_count + width + height NULL.
func TestD16a_SeriesImagesNullability(t *testing.T) {
	t.Parallel()
	notNull := map[string]bool{
		"id":         true,
		"series_id":  true,
		"language":   true,
		"kind":       true,
		"tmdb_path":  true,
		"position":   true,
		"updated_at": true,
	}
	nullable := map[string]bool{
		"asset_hash":   true,
		"iso_lang":     true,
		"vote_average": true,
		"vote_count":   true,
		"width":        true,
		"height":       true,
	}
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_images")
			for _, c := range tbl.Columns {
				if notNull[c.Name] && c.Type.Null {
					t.Errorf("col %q should be NOT NULL", c.Name)
				}
				if nullable[c.Name] && !c.Type.Null {
					t.Errorf("col %q should be NULL", c.Name)
				}
			}
		})
	}
}

// TestD16a_SeriesImagesDialectParityShape — column-name set parity
// across PG and SQLite.
func TestD16a_SeriesImagesDialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)
	pgT := mustTable(t, pg, "series_images")
	sqT := mustTable(t, sq, "series_images")
	if len(pgT.Columns) != len(sqT.Columns) {
		t.Errorf("series_images col count drift: pg=%d sqlite=%d",
			len(pgT.Columns), len(sqT.Columns))
	}
	pgNames := map[string]bool{}
	for _, c := range pgT.Columns {
		pgNames[c.Name] = true
	}
	for _, c := range sqT.Columns {
		if !pgNames[c.Name] {
			t.Errorf("col %q present in SQLite but missing in PG", c.Name)
		}
	}
}
