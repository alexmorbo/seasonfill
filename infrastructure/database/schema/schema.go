// Package schema is the single source-of-truth declarative target schema
// for the seasonfill database. It is consumed by the Atlas CLI at dev-time
// via `atlas migrate diff` to generate per-dialect SQL migrations under
// infrastructure/database/migrations/{postgres,sqlite}/.
//
// Runtime migrations are NOT applied via this package directly — production
// uses golang-migrate to replay the generated SQL files. See PRD §6.6
// Database Portability Contract and §D-1.
//
// Sub-stories 455..460 (D-1-2..D-1-7) populate this schema with the 14
// target tables. D-1-1 ships an intentionally empty schema as the seam
// between tooling and content.
package schema

import (
	atlasschema "ariga.io/atlas/sql/schema"
)

// SchemaName is exported for tests + Atlas provider integration; mirrors
// the literal passed to atlasschema.New below. Kept as a const so future
// renames (e.g., to a tenant-specific schema) only touch one place.
const SchemaName = "public"

// Schema returns the declarative target schema for the seasonfill database.
//
// In D-1-1 the returned schema is named "public" and contains zero tables —
// this is the seam between Atlas tooling (this story) and the first batch
// of tables (D-1-2). Running `atlas migrate diff init --env postgres` against
// this empty schema produces an empty (or no-op) migration, which is the
// expected behavior: subsequent sub-stories add tables and produce real
// migration SQL.
//
// Callers (Atlas CLI via external provider, dev-time tests in this package)
// must treat the returned *atlasschema.Schema as read-only — mutations should
// happen by editing this function, never by mutating the returned value.
func Schema() *atlasschema.Schema {
	s := atlasschema.New(SchemaName)

	// D-1-2 (story 455) lands the first table batch here: series, seasons,
	// episodes. Each subsequent sub-story appends its slice. Keep tables
	// grouped by domain and add a // <story-id> marker comment above each
	// group so reviewers can trace what landed when.

	return s
}
