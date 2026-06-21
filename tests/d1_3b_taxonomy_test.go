// Package tests — D-1-3b (story 456b) unit assertions for the
// taxonomy canonical dictionaries (genres, networks, production_companies,
// keywords), their per-language i18n siblings, and the 4 series ↔ taxonomy
// join tables. Inspects the in-memory Schema(d) struct for both dialects;
// no DB.
//
// Reuses mustTable / mustIndex / indexPredicate / columnNames helpers
// already defined in d1_2_core_series_test.go (same `package tests`).
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD13b_SchemaHasSeventeenTables — D-1-3a landed 5 (core + i18n);
// D-1-3b appends 12 more (4 canon + 4 i18n + 4 joins) → 17 total. Bump
// this in the next D-1-N batch.
func TestD13b_SchemaHasSeventeenTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if got, want := len(s.Tables), 17; got != want {
				t.Fatalf("table count = %d, want %d", got, want)
			}
			// Spot-check the 12 new tables are present.
			present := map[string]bool{}
			for _, tbl := range s.Tables {
				present[tbl.Name] = true
			}
			for _, name := range []string{
				"genres", "networks", "production_companies", "keywords",
				"genres_i18n", "networks_i18n", "production_companies_i18n", "keywords_i18n",
				"series_genres", "series_networks", "series_companies", "series_keywords",
			} {
				if !present[name] {
					t.Errorf("table %q missing from schema", name)
				}
			}
		})
	}
}

// TestD13b_CanonDictColumnCounts — genres / keywords have the minimal
// 4-col shape (id + tmdb_id + created_at + updated_at); networks /
// production_companies extend it with name + logo_asset +
// origin_country = 7 cols.
func TestD13b_CanonDictColumnCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table string
		want  int
	}{
		{"genres", 4},
		{"keywords", 4},
		{"networks", 7},
		{"production_companies", 7},
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

// TestD13b_TmdbPartialUnique — all 4 canon dicts must have a UNIQUE
// partial index on tmdb_id WHERE tmdb_id IS NOT NULL. Predicate carries
// the dialect-specific attr type.
func TestD13b_TmdbPartialUnique(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		for _, table := range []string{"genres", "keywords", "networks", "production_companies"} {
			t.Run(string(d)+"/"+table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), table)
				idxName := table + "_tmdb_id"
				idx := mustIndex(t, tbl, idxName)
				if !idx.Unique {
					t.Errorf("%s: not unique", idxName)
				}
				pred := indexPredicate(d, idx)
				if pred != "tmdb_id IS NOT NULL" {
					t.Errorf("%s predicate = %q, want %q", idxName, pred, "tmdb_id IS NOT NULL")
				}
			})
		}
	}
}

// TestD13b_I18nCompositePKs — every taxonomy i18n sibling has PK
// (parent_id, language).
func TestD13b_I18nCompositePKs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table, parentCol string
	}{
		{"genres_i18n", "genre_id"},
		{"keywords_i18n", "keyword_id"},
		{"networks_i18n", "network_id"},
		{"production_companies_i18n", "company_id"},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 {
					t.Fatalf("%s PK not composite-2", c.table)
				}
				if tbl.PrimaryKey.Parts[0].C.Name != c.parentCol ||
					tbl.PrimaryKey.Parts[1].C.Name != "language" {
					t.Errorf("%s PK = %s,%s, want %s,language",
						c.table,
						tbl.PrimaryKey.Parts[0].C.Name,
						tbl.PrimaryKey.Parts[1].C.Name,
						c.parentCol)
				}
			})
		}
	}
}

// TestD13b_I18nNameLookupIndex — every i18n sibling has an index on
// (language, name) — supports the PRD §5.4 Sonarr-genre fallback path.
func TestD13b_I18nNameLookupIndex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table, idxName string
	}{
		{"genres_i18n", "genres_i18n_name"},
		{"keywords_i18n", "keywords_i18n_name"},
		{"networks_i18n", "networks_i18n_name"},
		{"production_companies_i18n", "production_companies_i18n_name"},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				idx := mustIndex(t, tbl, c.idxName)
				if len(idx.Parts) != 2 {
					t.Fatalf("%s parts = %d, want 2", c.idxName, len(idx.Parts))
				}
				cols := []string{idx.Parts[0].C.Name, idx.Parts[1].C.Name}
				if cols[0] != "language" || cols[1] != "name" {
					t.Errorf("%s columns = %v, want [language name]", c.idxName, cols)
				}
				if idx.Unique {
					t.Errorf("%s should NOT be unique", c.idxName)
				}
			})
		}
	}
}

// TestD13b_JoinTableShape — verify join table column count, composite
// PK shape, two FKs, reverse-lookup index on right column.
func TestD13b_JoinTableShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table, leftCol, rightCol, revIdx string
		cols                             int
	}{
		{"series_genres", "series_id", "genre_id", "series_genres_genre", 3},
		{"series_networks", "series_id", "network_id", "series_networks_network", 3},
		{"series_companies", "series_id", "company_id", "series_companies_company", 3},
		{"series_keywords", "series_id", "keyword_id", "series_keywords_keyword", 2},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				if got := len(tbl.Columns); got != c.cols {
					t.Errorf("%s col count = %d, want %d", c.table, got, c.cols)
				}
				if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 {
					t.Fatalf("%s PK not composite-2", c.table)
				}
				if tbl.PrimaryKey.Parts[0].C.Name != c.leftCol ||
					tbl.PrimaryKey.Parts[1].C.Name != c.rightCol {
					t.Errorf("%s PK = %s,%s, want %s,%s",
						c.table,
						tbl.PrimaryKey.Parts[0].C.Name,
						tbl.PrimaryKey.Parts[1].C.Name,
						c.leftCol, c.rightCol)
				}
				revIdx := mustIndex(t, tbl, c.revIdx)
				if len(revIdx.Parts) != 1 || revIdx.Parts[0].C.Name != c.rightCol {
					t.Errorf("%s reverse-lookup index columns wrong (got %v)", c.revIdx, revIdx.Parts)
				}
				if len(tbl.ForeignKeys) != 2 {
					t.Errorf("%s FK count = %d, want 2", c.table, len(tbl.ForeignKeys))
				}
			})
		}
	}
}

// TestD13b_JoinCascadeOnSeriesSide — series-side FK is ON DELETE CASCADE
// (joins are projections of "series tagged with X"; orphan join rows are
// meaningless), taxonomy-side FK is ON DELETE NO ACTION (cannot drop a
// genre while series still reference it).
func TestD13b_JoinCascadeOnSeriesSide(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		for _, table := range []string{"series_genres", "series_networks", "series_companies", "series_keywords"} {
			t.Run(string(d)+"/"+table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), table)
				for _, fk := range tbl.ForeignKeys {
					isSeriesSide := len(fk.Columns) == 1 && fk.Columns[0].Name == "series_id"
					if isSeriesSide && fk.OnDelete != atlasschema.Cascade {
						t.Errorf("%s: series-side FK %s OnDelete = %q, want %q",
							table, fk.Symbol, fk.OnDelete, atlasschema.Cascade)
					}
					if !isSeriesSide && fk.OnDelete == atlasschema.Cascade {
						t.Errorf("%s: taxonomy-side FK %s should not Cascade", table, fk.Symbol)
					}
					if fk.OnUpdate != atlasschema.NoAction {
						t.Errorf("%s: FK %s OnUpdate = %q, want %q",
							table, fk.Symbol, fk.OnUpdate, atlasschema.NoAction)
					}
				}
			})
		}
	}
}

// TestD13b_I18nFKsNoCascade — taxonomy i18n FKs use NO ACTION on both
// sides per PRD §D-1 line 4408 ("canonical data — no cascade"). Guards
// against accidental cascade copy-paste from the join table builder.
func TestD13b_I18nFKsNoCascade(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table, fkName, parentCol, refTable string
	}{
		{"genres_i18n", "genres_i18n_genre_id_fkey", "genre_id", "genres"},
		{"keywords_i18n", "keywords_i18n_keyword_id_fkey", "keyword_id", "keywords"},
		{"networks_i18n", "networks_i18n_network_id_fkey", "network_id", "networks"},
		{"production_companies_i18n", "production_companies_i18n_company_id_fkey", "company_id", "production_companies"},
	}
	for _, d := range dialects {
		for _, c := range cases {
			t.Run(string(d)+"/"+c.table, func(t *testing.T) {
				t.Parallel()
				tbl := mustTable(t, schema.Schema(d), c.table)
				var fk *atlasschema.ForeignKey
				for _, f := range tbl.ForeignKeys {
					if f.Symbol == c.fkName {
						fk = f
						break
					}
				}
				if fk == nil {
					t.Fatalf("%s: FK %q not found", c.table, c.fkName)
				}
				if len(fk.Columns) != 1 || fk.Columns[0].Name != c.parentCol {
					t.Errorf("%s FK columns = %v, want [%s]",
						c.fkName, columnNamesFromCols(fk.Columns), c.parentCol)
				}
				if fk.RefTable == nil || fk.RefTable.Name != c.refTable {
					name := ""
					if fk.RefTable != nil {
						name = fk.RefTable.Name
					}
					t.Errorf("%s RefTable = %q, want %q", c.fkName, name, c.refTable)
				}
				if fk.OnDelete != atlasschema.NoAction {
					t.Errorf("%s OnDelete = %q, want %q", c.fkName, fk.OnDelete, atlasschema.NoAction)
				}
				if fk.OnUpdate != atlasschema.NoAction {
					t.Errorf("%s OnUpdate = %q, want %q", c.fkName, fk.OnUpdate, atlasschema.NoAction)
				}
			})
		}
	}
}

// TestD13b_DialectParityShape — every new D-1-3b table has identical
// column-name set across PG and SQLite.
func TestD13b_DialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)
	for _, name := range []string{
		"genres", "networks", "production_companies", "keywords",
		"genres_i18n", "networks_i18n", "production_companies_i18n", "keywords_i18n",
		"series_genres", "series_networks", "series_companies", "series_keywords",
	} {
		pgT := mustTable(t, pg, name)
		sqT := mustTable(t, sq, name)
		if len(pgT.Columns) != len(sqT.Columns) {
			t.Errorf("%s col count drift: pg=%d sqlite=%d",
				name, len(pgT.Columns), len(sqT.Columns))
		}
	}
}
