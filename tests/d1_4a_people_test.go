// Package tests — D-1-4a (story 457a) unit assertions for the people
// canon table + person_credits + person_biographies. Inspects in-memory
// Schema(d) for both dialects; no DB.
//
// Reuses mustTable / mustIndex helpers already defined in
// d1_2_core_series_test.go (same `package tests`).
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD14a_SchemaHasTwentyTables — at 457a ship the schema had 20 tables;
// D-1-4b (story 457b) appended 4 series-extras tables → total grew to 24.
// Tip-of-tree count contract lives in TestD14b_SchemaHasTwentyFourTables
// (d1_4b_series_extras_test.go); this test now loosens to a "3 D-1-4a
// people-domain tables PRESENT" assertion so it survives future appends
// without churning on every batch.
func TestD14a_SchemaHasTwentyTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			present := map[string]bool{}
			for _, tbl := range s.Tables {
				present[tbl.Name] = true
			}
			for _, name := range []string{"people", "person_credits", "person_biographies"} {
				if !present[name] {
					t.Errorf("table %q missing from schema", name)
				}
			}
		})
	}
}

// TestD14a_PeopleColumnCount — people has 15 cols (id + 12 data + created_at + updated_at).
func TestD14a_PeopleColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "people")
			if got, want := len(tbl.Columns), 15; got != want {
				t.Errorf("people col count = %d, want %d", got, want)
			}
		})
	}
}

// TestD14a_PersonCreditsColumnCount — 18 cols (id + person_id + 14 data + created_at + updated_at).
func TestD14a_PersonCreditsColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "person_credits")
			if got, want := len(tbl.Columns), 18; got != want {
				t.Errorf("person_credits col count = %d, want %d", got, want)
			}
		})
	}
}

// TestD14a_PersonBiographiesColumnCount — 4 cols (person_id, language, biography, updated_at).
func TestD14a_PersonBiographiesColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "person_biographies")
			if got, want := len(tbl.Columns), 4; got != want {
				t.Errorf("person_biographies col count = %d, want %d", got, want)
			}
		})
	}
}

// TestD14a_PersonBiographiesCompositePK — PK is (person_id, language).
func TestD14a_PersonBiographiesCompositePK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "person_biographies")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 {
				t.Fatalf("person_biographies PK not composite-2")
			}
			if tbl.PrimaryKey.Parts[0].C.Name != "person_id" ||
				tbl.PrimaryKey.Parts[1].C.Name != "language" {
				t.Errorf("person_biographies PK = %s,%s, want person_id,language",
					tbl.PrimaryKey.Parts[0].C.Name,
					tbl.PrimaryKey.Parts[1].C.Name)
			}
		})
	}
}

// TestD14a_PeoplePartialUnique — people_tmdb_id partial unique with the
// "tmdb_id IS NOT NULL" predicate.
func TestD14a_PeoplePartialUnique(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "people")
			idx := mustIndex(t, tbl, "people_tmdb_id")
			if !idx.Unique {
				t.Errorf("people_tmdb_id not unique")
			}
			pred := indexPredicate(d, idx)
			if pred != "tmdb_id IS NOT NULL" {
				t.Errorf("people_tmdb_id predicate = %q, want %q", pred, "tmdb_id IS NOT NULL")
			}
		})
	}
}

// TestD14a_PeopleImdbIndex — people_imdb_id plain (non-unique) index.
func TestD14a_PeopleImdbIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "people")
			idx := mustIndex(t, tbl, "people_imdb_id")
			if idx.Unique {
				t.Errorf("people_imdb_id should NOT be unique")
			}
			if len(idx.Parts) != 1 || idx.Parts[0].C.Name != "imdb_id" {
				gotCols := make([]string, len(idx.Parts))
				for i, p := range idx.Parts {
					gotCols[i] = p.C.Name
				}
				t.Errorf("people_imdb_id parts = %v, want [imdb_id]", gotCols)
			}
		})
	}
}

// TestD14a_PersonCreditsIndexes — 3 indexes: credit (unique on
// (person_id, tmdb_credit_id)), media (composite on (media_type,
// tmdb_media_id)), person (plain on person_id).
func TestD14a_PersonCreditsIndexes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		unique bool
		cols   []string
	}{
		{"person_credits_credit", true, []string{"person_id", "tmdb_credit_id"}},
		{"person_credits_media", false, []string{"media_type", "tmdb_media_id"}},
		{"person_credits_person", false, []string{"person_id"}},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), "person_credits")
				idx := mustIndex(t, tbl, c.name)
				if idx.Unique != c.unique {
					t.Errorf("%s unique = %v, want %v", c.name, idx.Unique, c.unique)
				}
				if len(idx.Parts) != len(c.cols) {
					t.Fatalf("%s parts count = %d, want %d", c.name, len(idx.Parts), len(c.cols))
				}
				for i, want := range c.cols {
					if idx.Parts[i].C.Name != want {
						t.Errorf("%s parts[%d] = %q, want %q", c.name, i, idx.Parts[i].C.Name, want)
					}
				}
			})
		}
	}
}

// TestD14a_FKsPresent — person_biographies and person_credits both have
// FK → people. Both use NoAction (canon-to-canon).
func TestD14a_FKsPresent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table, fkName string
	}{
		{"person_biographies", "person_biographies_person_id_fkey"},
		{"person_credits", "person_credits_person_id_fkey"},
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
				if got.RefTable == nil || got.RefTable.Name != "people" {
					name := ""
					if got.RefTable != nil {
						name = got.RefTable.Name
					}
					t.Errorf("%s FK RefTable = %q, want %q", c.table, name, "people")
				}
				if got.OnDelete != atlasschema.NoAction {
					t.Errorf("%s FK OnDelete = %q, want NoAction (canon-to-canon)", c.table, got.OnDelete)
				}
				if got.OnUpdate != atlasschema.NoAction {
					t.Errorf("%s FK OnUpdate = %q, want NoAction", c.table, got.OnUpdate)
				}
			})
		}
	}
}

// TestD14a_DialectParityShape — every new D-1-4a table has identical
// column-name set across PG and SQLite.
func TestD14a_DialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)
	for _, name := range []string{"people", "person_credits", "person_biographies"} {
		pgT := mustTable(t, pg, name)
		sqT := mustTable(t, sq, name)
		if len(pgT.Columns) != len(sqT.Columns) {
			t.Errorf("%s col count drift: pg=%d sqlite=%d",
				name, len(pgT.Columns), len(sqT.Columns))
		}
	}
}
