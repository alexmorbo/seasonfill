// Package tests holds cross-package smoke and lint-rule tests that don't
// belong inside any single bounded context. This file pins the D-1-2
// core-series seam: the dialect-parameterized schema.Schema(d) must
// produce a coherent target containing series + seasons + episodes for
// both Postgres and SQLite, with matching shape and the right partial-
// index attrs per dialect.
//
// Story 455 (D-1-2) — no DB, no integration tag, runs in the default
// `go test ./...` unit job.
package tests

import (
	"sort"
	"testing"

	"ariga.io/atlas/sql/postgres"
	atlasschema "ariga.io/atlas/sql/schema"
	"ariga.io/atlas/sql/sqlite"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// dialects covers every dialect we ship. Each test case parametrizes
// over this slice; if a future story adds MySQL, we extend this slice
// and the assertion matrix grows automatically.
var dialects = []schema.Dialect{schema.DialectPostgres, schema.DialectSQLite}

// TestD12_SchemaReturnsNonNil — preserved from D-1-1; parameterized to
// the new dialect signature.
func TestD12_SchemaReturnsNonNil(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			if schema.Schema(d) == nil {
				t.Fatalf("schema.Schema(%q) = nil; want non-nil *atlasschema.Schema", d)
			}
		})
	}
}

// TestD12_SchemaNameMatchesContract pins the schema name as "public" on
// both dialects. The const SchemaName must match.
func TestD12_SchemaNameMatchesContract(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			got := schema.Schema(d)
			if got.Name != "public" {
				t.Errorf("schema name = %q, want %q", got.Name, "public")
			}
		})
	}
	if schema.SchemaName != "public" {
		t.Errorf("SchemaName const = %q, want %q", schema.SchemaName, "public")
	}
}

// TestD12_SchemaHasThreeCoreTables inverts the D-1-1 "no tables yet"
// assertion: D-1-2 lands series/seasons/episodes. Adding a 4th core
// table here must come from D-1-3+; bump this test then.
func TestD12_SchemaHasThreeCoreTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if len(s.Tables) != 3 {
				t.Fatalf("table count = %d, want 3 (series, seasons, episodes)", len(s.Tables))
			}
			names := []string{s.Tables[0].Name, s.Tables[1].Name, s.Tables[2].Name}
			sort.Strings(names)
			want := []string{"episodes", "seasons", "series"}
			for i := range names {
				if names[i] != want[i] {
					t.Errorf("tables[%d] = %q, want %q (sorted)", i, names[i], want[i])
				}
			}
		})
	}
}

// TestD12_SeriesColumnCount asserts the canonical 32-column count per
// PRD §4.1. Drift indicates a renamed/added column that should require
// a follow-up story revision.
func TestD12_SeriesColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series")
			if len(tbl.Columns) != 32 {
				t.Errorf("series column count = %d, want 32", len(tbl.Columns))
			}
		})
	}
}

// TestD12_SeasonsColumnCount asserts the canonical 11-column count
// (id + 10 domain columns) for the seasons table.
func TestD12_SeasonsColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "seasons")
			if len(tbl.Columns) != 11 {
				t.Errorf("seasons column count = %d, want 11", len(tbl.Columns))
			}
		})
	}
}

// TestD12_EpisodesColumnCount asserts the canonical 17-column count for
// the episodes table.
func TestD12_EpisodesColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "episodes")
			if len(tbl.Columns) != 17 {
				t.Errorf("episodes column count = %d, want 17", len(tbl.Columns))
			}
		})
	}
}

// TestD12_PartialUniqueIndexOnTmdbId verifies the partial unique
// predicate attaches via the correct dialect attr type. This pins the
// pkg's dialect-aware index helper (it would silently break if the
// helper attached the wrong attr type).
func TestD12_PartialUniqueIndexOnTmdbId(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series")
			idx := mustIndex(t, tbl, "series_tmdb_id_idx")
			if !idx.Unique {
				t.Errorf("series_tmdb_id_idx not unique")
			}
			gotPred := indexPredicate(d, idx)
			if gotPred != "tmdb_id IS NOT NULL" {
				t.Errorf("partial-index predicate = %q, want %q", gotPred, "tmdb_id IS NOT NULL")
			}
		})
	}
}

// TestD12_PartialNonUniqueIndexOnTmdbType — the tmdb_type filter is a
// non-unique partial b-tree (multiple series can share a TMDB type).
// Guards against accidentally making it unique.
func TestD12_PartialNonUniqueIndexOnTmdbType(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "series")
			idx := mustIndex(t, tbl, "series_tmdb_type_idx")
			if idx.Unique {
				t.Errorf("series_tmdb_type_idx should be non-unique")
			}
			gotPred := indexPredicate(d, idx)
			if gotPred != "tmdb_type IS NOT NULL" {
				t.Errorf("partial-index predicate = %q, want %q", gotPred, "tmdb_type IS NOT NULL")
			}
		})
	}
}

// TestD12_DialectParityShape pins the SHAPE invariant: same table
// names, same columns per table, same indexes by name. SQL-side type
// literals are allowed to differ; we don't inspect them here.
func TestD12_DialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)

	if len(pg.Tables) != len(sq.Tables) {
		t.Fatalf("table count drift: pg=%d sqlite=%d", len(pg.Tables), len(sq.Tables))
	}

	for _, tblName := range []string{"series", "seasons", "episodes"} {
		pgT := mustTable(t, pg, tblName)
		sqT := mustTable(t, sq, tblName)
		if len(pgT.Columns) != len(sqT.Columns) {
			t.Errorf("%s column count drift: pg=%d sqlite=%d",
				tblName, len(pgT.Columns), len(sqT.Columns))
		}
		pgCols := columnNames(pgT)
		sqCols := columnNames(sqT)
		sort.Strings(pgCols)
		sort.Strings(sqCols)
		for i := range pgCols {
			if pgCols[i] != sqCols[i] {
				t.Errorf("%s column #%d drift: pg=%q sqlite=%q",
					tblName, i, pgCols[i], sqCols[i])
			}
		}
	}
}

// TestD12_UnknownDialectPanics guards Load() / Schema(d) from silent
// emission of empty schemas when ATLAS_DIALECT is misspelled.
func TestD12_UnknownDialectPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("Schema(\"mysql\") did not panic")
		}
	}()
	_ = schema.Schema("mysql")
}

// TestD12_SchemaDeterministic pins that two back-to-back calls return
// equivalent schemas — Atlas's diff engine assumes the schema is a
// pure function of the source.
func TestD12_SchemaDeterministic(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			first := schema.Schema(d)
			second := schema.Schema(d)
			if first.Name != second.Name {
				t.Errorf("non-deterministic schema name: %q vs %q", first.Name, second.Name)
			}
			if len(first.Tables) != len(second.Tables) {
				t.Errorf("non-deterministic table count: %d vs %d", len(first.Tables), len(second.Tables))
			}
		})
	}
}

// TestD12_AtlasSchemaTypeAvailable proves the atlas-go SDK import is
// wired correctly — if the dep is dropped from go.mod the test file
// refuses to compile and the entire `tests` package fails to build.
func TestD12_AtlasSchemaTypeAvailable(t *testing.T) {
	t.Parallel()
	got := atlasschema.New(schema.SchemaName)
	if got == nil {
		t.Fatalf("atlasschema.New returned nil; SDK contract broken")
	}
	if got.Name != schema.SchemaName {
		t.Errorf("atlasschema.New(%q).Name = %q", schema.SchemaName, got.Name)
	}
}

// --- helpers ---

func mustTable(t testing.TB, s *atlasschema.Schema, name string) *atlasschema.Table {
	t.Helper()
	for _, tbl := range s.Tables {
		if tbl.Name == name {
			return tbl
		}
	}
	t.Fatalf("table %q not found", name)
	return nil
}

func mustIndex(t testing.TB, tbl *atlasschema.Table, name string) *atlasschema.Index {
	t.Helper()
	for _, idx := range tbl.Indexes {
		if idx.Name == name {
			return idx
		}
	}
	t.Fatalf("index %q not found on table %q", name, tbl.Name)
	return nil
}

func columnNames(t *atlasschema.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// indexPredicate returns the partial-index predicate string for the
// given dialect, or "" if not present. The attr type differs by
// dialect — postgres.IndexPredicate vs sqlite.IndexPredicate.
func indexPredicate(d schema.Dialect, idx *atlasschema.Index) string {
	switch d {
	case schema.DialectPostgres:
		for _, a := range idx.Attrs {
			if p, ok := a.(*postgres.IndexPredicate); ok {
				return p.P
			}
		}
	case schema.DialectSQLite:
		for _, a := range idx.Attrs {
			if p, ok := a.(*sqlite.IndexPredicate); ok {
				return p.P
			}
		}
	}
	return ""
}
