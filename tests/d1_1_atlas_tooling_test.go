// Package tests holds cross-package smoke and lint-rule tests that don't
// belong inside any single bounded context. This file pins the D-1-1
// Atlas-tooling seam: the empty schema.Schema() must be coherent (named,
// non-nil, deterministic) so subsequent sub-stories can append tables
// without worrying about the foundation.
//
// Story 454 (D-1-1) — no DB, no integration tag, runs in the default
// `go test ./...` unit job.
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD11_SchemaReturnsNonNil pins the basic seam: Schema() returns a
// usable *schema.Schema, not a nil pointer. A regression here would
// break every sub-story that follows (D-1-2 expects to append tables).
func TestD11_SchemaReturnsNonNil(t *testing.T) {
	t.Parallel()

	got := schema.Schema()
	if got == nil {
		t.Fatalf("schema.Schema() returned nil; want non-nil *atlasschema.Schema")
	}
}

// TestD11_SchemaNameMatchesContract pins that we ship schema name
// "public" — the PRD §6.6 / §D-1 literal. Changing this name forces a
// migration on every Postgres deploy and silently breaks SQLite (which
// doesn't have schema namespaces); keeping it constant is load-bearing.
func TestD11_SchemaNameMatchesContract(t *testing.T) {
	t.Parallel()

	got := schema.Schema()
	if got.Name != "public" {
		t.Errorf("schema name = %q, want %q", got.Name, "public")
	}
	if schema.SchemaName != "public" {
		t.Errorf("SchemaName const = %q, want %q", schema.SchemaName, "public")
	}
}

// TestD11_SchemaHasNoTablesYet pins the D-1-1 seam: zero tables. When
// D-1-2 lands and adds series/seasons/episodes, this test MUST be
// updated to assert the new count (3). If it fails on the D-1-1 branch
// the seam has drifted — abort and re-plan.
func TestD11_SchemaHasNoTablesYet(t *testing.T) {
	t.Parallel()

	got := schema.Schema()
	if len(got.Tables) != 0 {
		t.Errorf("schema.Tables count = %d, want 0 in D-1-1 (tables land in D-1-2 onwards)", len(got.Tables))
	}
}

// TestD11_SchemaDeterministic pins that two back-to-back calls return
// equivalent schemas. Atlas's diff engine assumes the schema is a pure
// function of the source; a hidden global counter or once.Do leak would
// generate phantom migrations on the second `atlas migrate diff` run.
// This test catches that class of bug at unit-test cost.
func TestD11_SchemaDeterministic(t *testing.T) {
	t.Parallel()

	first := schema.Schema()
	second := schema.Schema()

	if first.Name != second.Name {
		t.Errorf("non-deterministic schema name: %q vs %q", first.Name, second.Name)
	}
	if len(first.Tables) != len(second.Tables) {
		t.Errorf("non-deterministic table count: %d vs %d", len(first.Tables), len(second.Tables))
	}
}

// TestD11_AtlasSchemaTypeAvailable proves the atlas-go SDK import is
// wired correctly — if the dep isn't in go.mod, the test file refuses
// to compile and the entire `tests` package fails to build, surfacing
// the missing dep loud and early. Cheap insurance.
func TestD11_AtlasSchemaTypeAvailable(t *testing.T) {
	t.Parallel()

	// atlasNew echoes the SDK's constructor signature, pinning the import
	// at compile time. If ariga.io/atlas drops out of go.mod, this file
	// fails to build and the whole tests package goes red.
	atlasNew := atlasschema.New
	got := atlasNew(schema.SchemaName)
	if got == nil {
		t.Fatalf("atlasschema.New returned nil; SDK contract broken")
	}
	if got.Name != schema.SchemaName {
		t.Errorf("atlasschema.New(%q).Name = %q", schema.SchemaName, got.Name)
	}
}
