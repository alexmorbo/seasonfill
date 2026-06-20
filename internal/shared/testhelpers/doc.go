// Package testhelpers hosts the shared test fixture for Phase 2's dual-backend
// repository test pattern (PRD §6 D-0 / A-4).
//
// Today (story 422): only the Postgres testcontainers helper lives here.
// Tomorrow (story 423): AllBackends(t) joins, returning [SQLite, Postgres]
// pairs that every repository test will range over.
//
// Local-dev override: SEASONFILL_TEST_POSTGRES_DSN bypasses container boot
// and points the helper at an existing Postgres. Matches the prior-art
// convention from infrastructure/database/migrations_test.go:299.
//
// Ryuk note: testcontainers ships a reaper sidecar that cleans up orphaned
// containers if the test process dies. On macOS Docker Desktop set
// TESTCONTAINERS_RYUK_DISABLED=true in .envrc if the privileged-container
// warning is noisy locally; no code change required.
package testhelpers
