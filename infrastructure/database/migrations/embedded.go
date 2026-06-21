// Package migrations exposes the D-1 dual-dialect SQL migrations as
// embed.FS variables consumable by the runtime migrator at
// internal/shared/db/migrations.go. The embed declaration lives here
// so the consumer package can import via the module graph without
// violating Go embed's no-".." rule.
package migrations

import "embed"

//go:embed postgres/*.sql
var Postgres embed.FS

//go:embed sqlite/*.sql
var SQLite embed.FS
