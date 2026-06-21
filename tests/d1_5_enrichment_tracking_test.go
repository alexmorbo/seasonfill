// Package tests — D-1-5 (story 458) unit assertions for the
// enrichment_errors side-table (000008 single-table migration).
// Inspects in-memory Schema(d) for both dialects; no DB.
package tests

import (
	"testing"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD15_EnrichmentErrorsPresent — enrichment_errors appears in the
// schema after D-1-5 (single table, separate from projections).
func TestD15_EnrichmentErrorsPresent(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			present := false
			for _, tbl := range s.Tables {
				if tbl.Name == "enrichment_errors" {
					present = true
					break
				}
			}
			if !present {
				t.Errorf("enrichment_errors missing from schema")
			}
		})
	}
}

// TestD15_EnrichmentErrorsColumnCount — 9 columns (id, entity_type,
// entity_id, source, last_error, attempts, first_seen_at,
// last_seen_at, next_attempt_at).
func TestD15_EnrichmentErrorsColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "enrichment_errors")
			if got, want := len(tbl.Columns), 9; got != want {
				t.Errorf("enrichment_errors col count = %d, want %d", got, want)
			}
		})
	}
}

// TestD15_EnrichmentErrorsSinglePK — single surrogate id PK (NOT a
// composite; matches videos/people pattern).
func TestD15_EnrichmentErrorsSinglePK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "enrichment_errors")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 {
				gotN := 0
				if tbl.PrimaryKey != nil {
					gotN = len(tbl.PrimaryKey.Parts)
				}
				t.Fatalf("enrichment_errors PK parts = %d, want 1", gotN)
			}
			if got := tbl.PrimaryKey.Parts[0].C.Name; got != "id" {
				t.Errorf("enrichment_errors PK col = %q, want id", got)
			}
		})
	}
}

// TestD15_EnrichmentErrorsUniqueComposite — UNIQUE composite-3 index
// on (entity_type, entity_id, source).
func TestD15_EnrichmentErrorsUniqueComposite(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "enrichment_errors")
			idx := mustIndex(t, tbl, "enrichment_errors_entity_source")
			if !idx.Unique {
				t.Errorf("enrichment_errors_entity_source should be UNIQUE")
			}
			want := []string{"entity_type", "entity_id", "source"}
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

// TestD15_EnrichmentErrorsNextAttemptPartialIndex — partial index on
// next_attempt_at WHERE NOT NULL.
func TestD15_EnrichmentErrorsNextAttemptPartialIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "enrichment_errors")
			idx := mustIndex(t, tbl, "enrichment_errors_next_attempt")
			if idx.Unique {
				t.Errorf("enrichment_errors_next_attempt should NOT be unique")
			}
			if got := indexPredicate(d, idx); got != "next_attempt_at IS NOT NULL" {
				t.Errorf("predicate = %q, want %q", got, "next_attempt_at IS NOT NULL")
			}
		})
	}
}

// TestD15_EnrichmentErrorsNoFK — POLYMORPHIC; entity_id has NO FK by
// design (mirrors external_ids from D-1-4b). Failing this test means
// an FK was added by reflex.
func TestD15_EnrichmentErrorsNoFK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "enrichment_errors")
			if len(tbl.ForeignKeys) != 0 {
				t.Errorf("enrichment_errors should have NO FKs (polymorphic); got %d FKs", len(tbl.ForeignKeys))
			}
		})
	}
}
