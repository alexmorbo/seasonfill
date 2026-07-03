// Package tests — D-1-6b (story 459b) unit assertions for the
// sonarr_instance + instance_secret tables. Inspects in-memory
// Schema(d) for both dialects; no DB.
//
// Split from d1_6b_admin_services_test.go to keep each file focused.
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD16b_SchemaHasThirtyFourTables — D-1-6a had 29; D-1-6b adds 5
// (sonarr_instance, instance_secret, app_secret,
// external_service_config, external_service_quota_state) → 34.
// D-1-7a adds 2 more (users, user_instance_tags) → 36. D-1-7b adds
// 3 more (grab_records, episode_grabs, download_links) → 39. D-1-7c
// (story 460c) adds 2 watchdog tables (watchdog_state,
// watchdog_blacklist) → 41. D-4 story 465b adds scan_runs → 42.
// D-5 story 466b adds app_config + sonarr_instance_settings → 44.
// D-6 story 467a re-adds the grab audit trio (decisions, cooldowns,
// origin_releases) → 47. D-6 story 467c adds the qBit runtime
// quartet (qbit_settings, qbit_torrents, qbit_torrent_events,
// torrent_series_map) → 51. D-7 story 468c re-adds media_assets → 52.
// N-2a story 502 adds discovery_lists → 53. E-1 B3a (story 580) adds
// season_texts → 54. E-1 story 584a adds series_media_texts → 55.
// S-C2 adds season_media_texts → 56.
func TestD16b_SchemaHasThirtyFourTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if got, want := len(s.Tables), 56; got != want {
				t.Fatalf("table count = %d, want %d", got, want)
			}
			present := map[string]bool{}
			for _, tbl := range s.Tables {
				present[tbl.Name] = true
			}
			for _, name := range []string{
				"sonarr_instance", "instance_secret", "app_secret",
				"external_service_config", "external_service_quota_state",
			} {
				if !present[name] {
					t.Errorf("table %q missing from schema", name)
				}
			}
		})
	}
}

// TestD16b_SonarrInstancePKIsText — sonarr_instance.name is the TEXT PK
// (natural key, not surrogate BIGSERIAL).
func TestD16b_SonarrInstancePKIsText(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "sonarr_instance")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 {
				gotN := 0
				if tbl.PrimaryKey != nil {
					gotN = len(tbl.PrimaryKey.Parts)
				}
				t.Fatalf("sonarr_instance PK parts = %d, want 1", gotN)
			}
			pkCol := tbl.PrimaryKey.Parts[0].C
			if pkCol.Name != "name" {
				t.Errorf("sonarr_instance PK col = %q, want name", pkCol.Name)
			}
		})
	}
}

// TestD16b_SonarrInstanceColumnCount — 10 columns (name, url, public_url,
// mode, token_secret_id, health, last_check_at, transitions_count,
// created_at, updated_at).
func TestD16b_SonarrInstanceColumnCount(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "sonarr_instance")
			if got, want := len(tbl.Columns), 10; got != want {
				t.Errorf("sonarr_instance col count = %d, want %d", got, want)
			}
		})
	}
}

// TestD16b_SonarrInstanceTokenSecretFK — sonarr_instance.token_secret_id
// FK → instance_secret.id ON DELETE SET NULL (back-reference; instance
// row survives secret hard-delete).
func TestD16b_SonarrInstanceTokenSecretFK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "sonarr_instance")
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("sonarr_instance FK count = %d, want 1", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.OnDelete != atlasschema.SetNull {
				t.Errorf("sonarr_instance FK OnDelete = %q, want SetNull (back-ref survives secret hard-delete)", fk.OnDelete)
			}
			if fk.RefTable == nil || fk.RefTable.Name != "instance_secret" {
				name := ""
				if fk.RefTable != nil {
					name = fk.RefTable.Name
				}
				t.Errorf("sonarr_instance FK RefTable = %q, want instance_secret", name)
			}
		})
	}
}

// TestD16b_InstanceSecretPK — instance_secret.id is the single BIGSERIAL
// PK (surrogate; UNIQUE composite handles the natural key separately).
func TestD16b_InstanceSecretPK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "instance_secret")
			if tbl.PrimaryKey == nil || len(tbl.PrimaryKey.Parts) != 1 {
				gotN := 0
				if tbl.PrimaryKey != nil {
					gotN = len(tbl.PrimaryKey.Parts)
				}
				t.Fatalf("instance_secret PK parts = %d, want 1", gotN)
			}
			if got := tbl.PrimaryKey.Parts[0].C.Name; got != "id" {
				t.Errorf("instance_secret PK col = %q, want id", got)
			}
		})
	}
}

// TestD16b_InstanceSecretInstanceNameFK — instance_secret.instance_name
// FK → sonarr_instance.name ON DELETE CASCADE (forward-ref; secrets
// die with instance).
func TestD16b_InstanceSecretInstanceNameFK(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "instance_secret")
			if len(tbl.ForeignKeys) != 1 {
				t.Fatalf("instance_secret FK count = %d, want 1", len(tbl.ForeignKeys))
			}
			fk := tbl.ForeignKeys[0]
			if fk.OnDelete != atlasschema.Cascade {
				t.Errorf("instance_secret FK OnDelete = %q, want Cascade (forward-ref dies with instance)", fk.OnDelete)
			}
			if fk.RefTable == nil || fk.RefTable.Name != "sonarr_instance" {
				name := ""
				if fk.RefTable != nil {
					name = fk.RefTable.Name
				}
				t.Errorf("instance_secret FK RefTable = %q, want sonarr_instance", name)
			}
		})
	}
}

// TestD16b_InstanceSecretUniqueComposite — UNIQUE composite-2 index on
// (instance_name, secret_name) for the primary lookup path.
func TestD16b_InstanceSecretUniqueComposite(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "instance_secret")
			idx := mustIndex(t, tbl, "instance_secret_lookup")
			if !idx.Unique {
				t.Errorf("instance_secret_lookup should be UNIQUE")
			}
			want := []string{"instance_name", "secret_name"}
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

// TestD16b_InstanceSecretEncryptedValueIsBytes — encrypted_value column
// is BYTEA on PG / BLOB on SQLite (NOT text). Atlas maps `bytea` to
// BLOB on SQLite automatically.
func TestD16b_InstanceSecretEncryptedValueIsBytes(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "instance_secret")
			found := false
			for _, c := range tbl.Columns {
				if c.Name != "encrypted_value" {
					continue
				}
				found = true
				if c.Type.Null {
					t.Errorf("encrypted_value should be NOT NULL")
				}
				if _, ok := c.Type.Type.(*atlasschema.BinaryType); !ok {
					t.Errorf("encrypted_value type = %T, want *BinaryType", c.Type.Type)
				}
			}
			if !found {
				t.Errorf("encrypted_value column missing from instance_secret")
			}
		})
	}
}

// TestD16b_SonarrInstanceUnhealthyPartialIndex — partial index on
// last_check_at WHERE health <> 'healthy'.
func TestD16b_SonarrInstanceUnhealthyPartialIndex(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "sonarr_instance")
			idx := mustIndex(t, tbl, "sonarr_instance_unhealthy")
			if idx.Unique {
				t.Errorf("sonarr_instance_unhealthy should NOT be unique")
			}
			if got := indexPredicate(d, idx); got != "health <> 'healthy'" {
				t.Errorf("predicate = %q, want %q", got, "health <> 'healthy'")
			}
		})
	}
}
