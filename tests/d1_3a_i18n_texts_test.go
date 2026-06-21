// Package tests — D-1-3a (story 456a) unit assertions for the
// series_texts and episode_texts schema seam. Inspects the in-memory
// Schema(d) struct for both dialects; no DB.
package tests

import (
	"sort"
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD13a_SchemaHasFiveTables — D-1-2 landed 3; D-1-3a adds 2 more
// (series_texts, episode_texts). This is the current-tip total-count
// contract; bump as further D-1-N batches append tables.
func TestD13a_SchemaHasFiveTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if len(s.Tables) != 5 {
				t.Fatalf("table count = %d, want 5 (series, seasons, episodes, series_texts, episode_texts)", len(s.Tables))
			}
			names := make([]string, len(s.Tables))
			for i, tbl := range s.Tables {
				names[i] = tbl.Name
			}
			sort.Strings(names)
			want := []string{"episode_texts", "episodes", "seasons", "series", "series_texts"}
			for i := range names {
				if names[i] != want[i] {
					t.Errorf("tables[%d] = %q, want %q", i, names[i], want[i])
				}
			}
		})
	}
}

// TestD13a_SeriesTextsColumnCount — 7 cols per PRD §4.3:
// series_id, language, title, overview, tagline, enriched_at, updated_at.
func TestD13a_SeriesTextsColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_texts")
			if len(tbl.Columns) != 7 {
				t.Errorf("series_texts column count = %d, want 7", len(tbl.Columns))
			}
		})
	}
}

// TestD13a_EpisodeTextsColumnCount — 6 cols (no tagline on episodes).
func TestD13a_EpisodeTextsColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "episode_texts")
			if len(tbl.Columns) != 6 {
				t.Errorf("episode_texts column count = %d, want 6", len(tbl.Columns))
			}
		})
	}
}

// TestD13a_SeriesTextsCompositePK — PK is (series_id, language).
func TestD13a_SeriesTextsCompositePK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series_texts")
			if tbl.PrimaryKey == nil {
				t.Fatalf("series_texts: no primary key set")
			}
			if got, want := len(tbl.PrimaryKey.Parts), 2; got != want {
				t.Fatalf("series_texts PK parts = %d, want %d", got, want)
			}
			parts := []string{
				tbl.PrimaryKey.Parts[0].C.Name,
				tbl.PrimaryKey.Parts[1].C.Name,
			}
			if parts[0] != "series_id" || parts[1] != "language" {
				t.Errorf("series_texts PK = %v, want [series_id language]", parts)
			}
		})
	}
}

// TestD13a_EpisodeTextsCompositePK — PK is (episode_id, language).
func TestD13a_EpisodeTextsCompositePK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "episode_texts")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 {
				t.Fatalf("episode_texts PK not composite-2")
			}
			if tbl.PrimaryKey.Parts[0].C.Name != "episode_id" ||
				tbl.PrimaryKey.Parts[1].C.Name != "language" {
				t.Errorf("episode_texts PK = %s,%s, want episode_id,language",
					tbl.PrimaryKey.Parts[0].C.Name, tbl.PrimaryKey.Parts[1].C.Name)
			}
		})
	}
}

// TestD13a_FKsPresent — series_texts has FK to series; episode_texts to episodes.
func TestD13a_FKsPresent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table, fkName, parentCol, refTable string
	}{
		{"series_texts", "series_texts_series_id_fkey", "series_id", "series"},
		{"episode_texts", "episode_texts_episode_id_fkey", "episode_id", "episodes"},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				var got *atlasschema.ForeignKey
				for _, fk := range tbl.ForeignKeys {
					if fk.Symbol == c.fkName {
						got = fk
						break
					}
				}
				if got == nil {
					t.Fatalf("%s: FK %q not found", c.table, c.fkName)
				}
				if len(got.Columns) != 1 || got.Columns[0].Name != c.parentCol {
					t.Errorf("FK columns = %v, want [%s]", columnNamesFromCols(got.Columns), c.parentCol)
				}
				if got.RefTable == nil || got.RefTable.Name != c.refTable {
					name := ""
					if got.RefTable != nil {
						name = got.RefTable.Name
					}
					t.Errorf("FK RefTable = %q, want %q", name, c.refTable)
				}
				if got.OnDelete != atlasschema.NoAction {
					t.Errorf("FK OnDelete = %q, want NO ACTION", got.OnDelete)
				}
				if got.OnUpdate != atlasschema.NoAction {
					t.Errorf("FK OnUpdate = %q, want NO ACTION", got.OnUpdate)
				}
			})
		}
	}
}

// TestD13a_DialectParityShape — PG and SQLite must produce the same
// table+column shape for i18n tables (only SQL-side type literals differ).
func TestD13a_DialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)
	for _, name := range []string{"series_texts", "episode_texts"} {
		pgT := mustTable(t, pg, name)
		sqT := mustTable(t, sq, name)
		if len(pgT.Columns) != len(sqT.Columns) {
			t.Errorf("%s col count drift: pg=%d sqlite=%d", name, len(pgT.Columns), len(sqT.Columns))
		}
		pgC := columnNames(pgT)
		sqC := columnNames(sqT)
		sort.Strings(pgC)
		sort.Strings(sqC)
		for i := range pgC {
			if pgC[i] != sqC[i] {
				t.Errorf("%s col #%d drift: pg=%q sqlite=%q", name, i, pgC[i], sqC[i])
			}
		}
	}
}

// columnNamesFromCols returns the name slice for a []*atlasschema.Column
// — distinct from columnNames(*Table) which lives in d1_2_core_series_test.go.
func columnNamesFromCols(cols []*atlasschema.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}
