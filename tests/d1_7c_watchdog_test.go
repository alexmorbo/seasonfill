// Package tests — D-1-7c (story 460c) unit assertions for
// watchdog_state + watchdog_blacklist. Inspects in-memory Schema(d) for
// both dialects; no DB.
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

func TestD1_7c_WatchdogState_TableShape(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "watchdog_state")
			if got := len(tbl.Columns); got != 8 {
				t.Errorf("watchdog_state column count = %d, want 8", got)
			}
			pk := tbl.PrimaryKey
			if pk == nil || len(pk.Parts) != 3 {
				t.Fatalf("watchdog_state PK = %+v, want 3-col composite", pk)
			}
			for i, want := range []string{"instance_name", "sonarr_series_id", "season_number"} {
				if got := pk.Parts[i].C.Name; got != want {
					t.Errorf("PK part %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestD1_7c_WatchdogState_Indexes(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "watchdog_state")
			_ = mustIndex(t, tbl, "watchdog_state_instance_name_idx")
			cooldownIdx := mustIndex(t, tbl, "watchdog_state_cooldown_until_idx")
			if cooldownIdx.Unique {
				t.Errorf("watchdog_state_cooldown_until_idx should NOT be UNIQUE")
			}
			if got := indexPredicate(d, cooldownIdx); got != "cooldown_until IS NOT NULL" {
				t.Errorf("watchdog_state_cooldown_until_idx predicate = %q, want %q",
					got, "cooldown_until IS NOT NULL")
			}
		})
	}
}

func TestD1_7c_WatchdogState_ForeignKey(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "watchdog_state")
	if got := len(tbl.ForeignKeys); got != 1 {
		t.Fatalf("watchdog_state FK count = %d, want 1", got)
	}
	fk := tbl.ForeignKeys[0]
	if fk.Symbol != "watchdog_state_instance_name_fkey" {
		t.Errorf("FK symbol = %q, want watchdog_state_instance_name_fkey", fk.Symbol)
	}
	if fk.OnDelete != atlasschema.Cascade {
		t.Errorf("FK OnDelete = %s, want CASCADE", fk.OnDelete)
	}
	if fk.RefTable == nil || fk.RefTable.Name != "sonarr_instance" {
		t.Errorf("FK ref table = %v, want sonarr_instance", fk.RefTable)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0].Name != "name" {
		t.Errorf("FK ref columns = %v, want [name]", fk.RefColumns)
	}
}

func TestD1_7c_WatchdogState_ColumnNullability(t *testing.T) {
	t.Parallel()
	tbl := mustTable(t, schema.Schema(schema.DialectPostgres), "watchdog_state")

	type want struct {
		name     string
		nullable bool
	}
	checks := []want{
		{"instance_name", false},
		{"sonarr_series_id", false},
		{"season_number", false},
		{"attempt_count", false},
		{"last_attempt_at", false},
		{"cooldown_until", true},
		{"last_error", true},
		{"updated_at", false},
	}
	for _, w := range checks {
		col := findColumnInTable(tbl, w.name)
		if col == nil {
			t.Errorf("watchdog_state.%s missing", w.name)
			continue
		}
		if col.Type == nil {
			t.Errorf("watchdog_state.%s has nil Type", w.name)
			continue
		}
		if col.Type.Null != w.nullable {
			t.Errorf("watchdog_state.%s nullable = %v, want %v", w.name, col.Type.Null, w.nullable)
		}
	}

	// attempt_count DEFAULT 0
	attemptCount := findColumnInTable(tbl, "attempt_count")
	if attemptCount == nil || attemptCount.Default == nil {
		t.Errorf("watchdog_state.attempt_count must have DEFAULT 0; got %+v", attemptCount)
	} else if lit, ok := attemptCount.Default.(*atlasschema.Literal); !ok || lit.V != "0" {
		t.Errorf("watchdog_state.attempt_count default = %+v, want Literal '0'", attemptCount.Default)
	}
}

func TestD1_7c_WatchdogBlacklist_TableShape(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "watchdog_blacklist")
			if got := len(tbl.Columns); got != 8 {
				t.Errorf("watchdog_blacklist column count = %d, want 8", got)
			}
			pk := tbl.PrimaryKey
			if pk == nil || len(pk.Parts) != 3 {
				t.Fatalf("watchdog_blacklist PK = %+v, want 3-col composite", pk)
			}
			for i, want := range []string{"instance_name", "sonarr_series_id", "season_number"} {
				if got := pk.Parts[i].C.Name; got != want {
					t.Errorf("PK part %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestD1_7c_WatchdogBlacklist_Indexes(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "watchdog_blacklist")
			ttlIdx := mustIndex(t, tbl, "watchdog_blacklist_ttl_until_idx")
			if ttlIdx.Unique {
				t.Errorf("watchdog_blacklist_ttl_until_idx should NOT be UNIQUE")
			}
			if got := indexPredicate(d, ttlIdx); got != "ttl_until IS NOT NULL" {
				t.Errorf("watchdog_blacklist_ttl_until_idx predicate = %q, want %q",
					got, "ttl_until IS NOT NULL")
			}
		})
	}
}

func TestD1_7c_WatchdogBlacklist_ForeignKey(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "watchdog_blacklist")
	if got := len(tbl.ForeignKeys); got != 1 {
		t.Fatalf("watchdog_blacklist FK count = %d, want 1", got)
	}
	fk := tbl.ForeignKeys[0]
	if fk.Symbol != "watchdog_blacklist_instance_name_fkey" {
		t.Errorf("FK symbol = %q, want watchdog_blacklist_instance_name_fkey", fk.Symbol)
	}
	if fk.OnDelete != atlasschema.Cascade {
		t.Errorf("FK OnDelete = %s, want CASCADE", fk.OnDelete)
	}
	if fk.RefTable == nil || fk.RefTable.Name != "sonarr_instance" {
		t.Errorf("FK ref table = %v, want sonarr_instance", fk.RefTable)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0].Name != "name" {
		t.Errorf("FK ref columns = %v, want [name]", fk.RefColumns)
	}
}

func TestD1_7c_WatchdogBlacklist_ColumnNullability(t *testing.T) {
	t.Parallel()
	tbl := mustTable(t, schema.Schema(schema.DialectPostgres), "watchdog_blacklist")

	type want struct {
		name     string
		nullable bool
	}
	checks := []want{
		{"instance_name", false},
		{"sonarr_series_id", false},
		{"season_number", false},
		{"release_title", true},
		{"reason", false},
		{"consecutive", false},
		{"blacklisted_at", false},
		{"ttl_until", true},
	}
	for _, w := range checks {
		col := findColumnInTable(tbl, w.name)
		if col == nil {
			t.Errorf("watchdog_blacklist.%s missing", w.name)
			continue
		}
		if col.Type == nil {
			t.Errorf("watchdog_blacklist.%s has nil Type", w.name)
			continue
		}
		if col.Type.Null != w.nullable {
			t.Errorf("watchdog_blacklist.%s nullable = %v, want %v", w.name, col.Type.Null, w.nullable)
		}
	}

	// consecutive DEFAULT 0
	consecutive := findColumnInTable(tbl, "consecutive")
	if consecutive == nil || consecutive.Default == nil {
		t.Errorf("watchdog_blacklist.consecutive must have DEFAULT 0; got %+v", consecutive)
	} else if lit, ok := consecutive.Default.(*atlasschema.Literal); !ok || lit.V != "0" {
		t.Errorf("watchdog_blacklist.consecutive default = %+v, want Literal '0'", consecutive.Default)
	}
}
