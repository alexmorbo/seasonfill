// Package tests — D-1-6b (story 459b) unit assertions for the
// app_secret + external_service_config + external_service_quota_state
// tables. Inspects in-memory Schema(d) for both dialects; no DB.
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD16b_AppSecretPK — app_secret.id is the single BIGSERIAL PK.
func TestD16b_AppSecretPK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "app_secret")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 {
				t.Fatalf("app_secret PK parts != 1")
			}
			if got := tbl.PrimaryKey.Parts[0].C.Name; got != "id" {
				t.Errorf("app_secret PK col = %q, want id", got)
			}
		})
	}
}

// TestD16b_AppSecretUniqueName — secret_name UNIQUE (app-level singletons).
func TestD16b_AppSecretUniqueName(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "app_secret")
			idx := mustIndex(t, tbl, "app_secret_name")
			if !idx.Unique {
				t.Errorf("app_secret_name should be UNIQUE")
			}
			if len(idx.Parts) != 1 || idx.Parts[0].C.Name != "secret_name" {
				t.Errorf("app_secret_name parts wrong: %+v", idx.Parts)
			}
		})
	}
}

// TestD16b_AppSecretEncryptedValueIsBytes — encrypted_value is BYTEA/BLOB.
func TestD16b_AppSecretEncryptedValueIsBytes(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "app_secret")
			found := false
			for _, c := range tbl.Columns {
				if c.Name != "encrypted_value" {
					continue
				}
				found = true
				if c.Type.Null {
					t.Errorf("app_secret.encrypted_value should be NOT NULL")
				}
				if _, ok := c.Type.Type.(*atlasschema.BinaryType); !ok {
					t.Errorf("app_secret.encrypted_value type = %T, want *BinaryType", c.Type.Type)
				}
			}
			if !found {
				t.Errorf("encrypted_value column missing from app_secret")
			}
		})
	}
}

// TestD16b_ExternalServiceConfigPKIsText — service_name TEXT PK.
func TestD16b_ExternalServiceConfigPKIsText(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "external_service_config")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 {
				t.Fatalf("external_service_config PK parts != 1")
			}
			if got := tbl.PrimaryKey.Parts[0].C.Name; got != "service_name" {
				t.Errorf("external_service_config PK col = %q, want service_name", got)
			}
		})
	}
}

// TestD16b_ExternalServiceConfigTwoFKs — 2 FKs to app_secret.id, both
// SET NULL.
func TestD16b_ExternalServiceConfigTwoFKs(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "external_service_config")
			if len(tbl.ForeignKeys) != 2 {
				t.Fatalf("external_service_config FK count = %d, want 2", len(tbl.ForeignKeys))
			}
			for _, fk := range tbl.ForeignKeys {
				if fk.OnDelete != atlasschema.SetNull {
					t.Errorf("FK %q OnDelete = %q, want SetNull", fk.Symbol, fk.OnDelete)
				}
				if fk.RefTable == nil || fk.RefTable.Name != "app_secret" {
					name := ""
					if fk.RefTable != nil {
						name = fk.RefTable.Name
					}
					t.Errorf("FK %q RefTable = %q, want app_secret", fk.Symbol, name)
				}
			}
		})
	}
}

// TestD16b_ExternalServiceQuotaStateCompositePK — composite-2 PK
// (service_name, window_start).
func TestD16b_ExternalServiceQuotaStateCompositePK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "external_service_quota_state")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 2 {
				gotN := 0
				if tbl.PrimaryKey != nil {
					gotN = len(tbl.PrimaryKey.Parts)
				}
				t.Fatalf("external_service_quota_state PK parts = %d, want 2", gotN)
			}
			want := []string{"service_name", "window_start"}
			for i, w := range want {
				if tbl.PrimaryKey.Parts[i].C.Name != w {
					t.Errorf("PK parts[%d] = %q, want %q", i, tbl.PrimaryKey.Parts[i].C.Name, w)
				}
			}
		})
	}
}

// TestD16b_ExternalServiceQuotaStateWindowIndex — non-unique index on
// window_start (GC sweep path).
func TestD16b_ExternalServiceQuotaStateWindowIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "external_service_quota_state")
			idx := mustIndex(t, tbl, "external_service_quota_state_window")
			if idx.Unique {
				t.Errorf("external_service_quota_state_window should NOT be unique")
			}
			if len(idx.Parts) != 1 || idx.Parts[0].C.Name != "window_start" {
				t.Errorf("parts wrong: %+v", idx.Parts)
			}
		})
	}
}

// TestD16b_AdminDialectParityShape — column counts match across PG +
// SQLite for all 5 admin tables.
func TestD16b_AdminDialectParityShape(t *testing.T) {
	t.Parallel()
	pg := schema.Schema(schema.DialectPostgres)
	sq := schema.Schema(schema.DialectSQLite)
	for _, name := range []string{
		"sonarr_instance", "instance_secret", "app_secret",
		"external_service_config", "external_service_quota_state",
	} {
		pgT := mustTable(t, pg, name)
		sqT := mustTable(t, sq, name)
		if len(pgT.Columns) != len(sqT.Columns) {
			t.Errorf("%s col count drift: pg=%d sqlite=%d",
				name, len(pgT.Columns), len(sqT.Columns))
		}
	}
}

// TestD16b_NoCheckConstraints — admin tables use domain-enforced values
// (mode, health, kind, service_name). NO CHECK constraints per the
// schema's "validate at use-case layer" idiom.
func TestD16b_NoCheckConstraints(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			for _, name := range []string{
				"sonarr_instance", "instance_secret", "app_secret",
				"external_service_config", "external_service_quota_state",
			} {
				tbl := mustTable(t, schema.Schema(d), name)
				for _, attr := range tbl.Attrs {
					if _, ok := attr.(*atlasschema.Check); ok {
						t.Errorf("table %q has CHECK constraint (use-case layer enforcement only)", name)
					}
				}
			}
		})
	}
}
