//go:build integration

// D-8 closure test — Phase 2 (Data Model) acceptance gate.
//
// Locks in the post-cutover schema invariants the operator depends on
// before dropping+recreating the production database. The four guards:
//
//  1. All 27 migrations apply cleanly from an empty DB (no compound
//     failures, no partial-state crashes).
//  2. Every name in d1AcceptanceTablesPostgres (54 entries) is present
//     post-Up on both dialects — and no surprise extras snuck in.
//  3. No legacy table names survive — admin_users, app_settings,
//     sync_log, etc. were retired during D-3..D-7; this denylist
//     trips if a stray .up.sql resurrects one.
//  4. schema_migrations reports exactly 27 applied versions —
//     matches the 27 .up.sql files under
//     infrastructure/database/migrations/{postgres,sqlite}.
//
// Runs on the standard dual-backend rig (sqlite always, postgres
// opt-in via SEASONFILL_TEST_POSTGRES_ENABLE) — reuses the helpers
// already proving D-1's acceptance gate.
package integration

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// d8LegacyTableDenylist is the canonical "must not exist" set. Sourced
// from ADR D2-revised-roadmap.md §Decision-3 (D-2 deletes) +
// D-3 / D-4 / D-5 closure notes. If a future migration accidentally
// resurrects one of these (e.g. someone re-imports the old gorm models
// and the auto-migrate path fires), the closure gate trips immediately
// rather than waiting for a smoke session to spot it.
var d8LegacyTableDenylist = []string{
	"admin_users",               // D-5 → users
	"app_settings",              // D-5 → app_config
	"sync_log",                  // D-3 → enrichment_errors
	"external_service_settings", // D-5 → external_service_config
	"runtime_config",            // D-5 collapsed into app_config + sonarr_instance_settings
	"instance_qbit_settings",    // D-6 → qbit_settings (top-level, no instance prefix)
	"series_people",             // D-7 → person_credits (MediaType="tv")
	"episode_people",            // D-7 → person_credits (MediaType="tv_episode")
	"i18n_texts",                // Pre-D-1 single-table → split into series_texts / episode_texts / *_i18n siblings
	"instance_metadata",         // never made it past PRD draft — but guards future drift
	"user_sessions",             // D-5 deliberately skipped — stateless HMAC + epoch
	"user_settings",             // D-5 collapsed into users row
}

// TestD8_Closure_AllMigrationsApply runs the full migration chain from
// an empty DB on each backend. Asserts that Up() returns no error.
// Smaller than d1's TestD1_Acceptance_RuntimeUpDown — purely a "fresh
// boot" check, no Down() required (operator's runbook never invokes
// Down on a live DB).
func TestD8_Closure_AllMigrationsApply(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			_, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up(), "Up() failed on %s — D-8 closure gate", b.name)
		})
	}
}

// TestD8_Closure_TableInventoryMatches verifies that the live DB after
// Up() contains exactly the 54 names in d1AcceptanceTablesPostgres.
// A missing name means a migration regressed; an extra name means a
// legacy or one-off table snuck back in. ElementsMatch — order-free.
func TestD8_Closure_TableInventoryMatches(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up(), "Up() failed on %s", b.name)

			live := liveTableNames(t, ctx, db, b.name)
			require.ElementsMatchf(t, d1AcceptanceTablesPostgres, live,
				"table inventory drift on %s — expected %d tables, got %d",
				b.name, len(d1AcceptanceTablesPostgres), len(live))
		})
	}
}

// TestD8_Closure_NoLegacyTables verifies that none of the retired
// pre-D table names appear in the post-Up schema. Sister test to
// TableInventoryMatches — the inventory test catches extras at the
// "exact set" level; this test names the specific legacy ghosts the
// operator was burned by during D-2..D-7 and gives a clearer failure
// message ("legacy table sync_log resurrected" vs "extra: sync_log").
func TestD8_Closure_NoLegacyTables(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up(), "Up() failed on %s", b.name)

			live := liveTableNames(t, ctx, db, b.name)
			liveSet := make(map[string]struct{}, len(live))
			for _, n := range live {
				liveSet[n] = struct{}{}
			}

			var found []string
			for _, legacy := range d8LegacyTableDenylist {
				if _, ok := liveSet[legacy]; ok {
					found = append(found, legacy)
				}
			}
			sort.Strings(found)
			assert.Emptyf(t, found,
				"legacy table(s) resurrected on %s: %v — see ADR D2-revised-roadmap.md",
				b.name, found)
		})
	}
}

// TestD8_Closure_SchemaMigrationsHeadVersion verifies that after a
// full Up() the golang-migrate tracker table reports the highest
// expected version (27) with dirty=false. golang-migrate stores a
// single "current head" row (not a per-migration history), so the
// invariant is MAX(version)=27 AND dirty=false. Catches the "I forgot
// to add the new migration to the embed list" failure mode and the
// "Up() silently stopped after N" failure mode (which would leave a
// lower version pinned), plus the "previous run crashed mid-Up"
// failure mode (dirty=true, blocks subsequent migrations).
func TestD8_Closure_SchemaMigrationsHeadVersion(t *testing.T) {
	const wantHead = 27

	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up(), "Up() failed on %s", b.name)

			var (
				head  int
				dirty bool
			)
			require.NoError(t,
				db.QueryRowContext(ctx,
					`SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1`).
					Scan(&head, &dirty),
				"read head row from schema_migrations on %s", b.name)
			assert.Equalf(t, wantHead, head,
				"schema_migrations.version on %s = %d; want %d "+
					"(matches the 27 .up.sql files committed in 000001..000027)",
				b.name, head, wantHead)
			assert.Falsef(t, dirty,
				"schema_migrations.dirty=true on %s — a previous Up() crashed mid-migration; "+
					"manual `force` recovery required before next deploy", b.name)
		})
	}
}
