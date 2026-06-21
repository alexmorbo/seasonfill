// Package tests — D-1-7a (story 460a) unit assertions for the users +
// user_instance_tags tables. Inspects in-memory Schema(d) for both
// dialects; no DB.
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD17a_SchemaHasThirtySixTables — D-1-6b had 34; D-1-7a adds 2
// (users, user_instance_tags) → 36. D-1-7b later adds 3 more
// (grab_records, episode_grabs, download_links) → 39, so the live
// assertion below is on 39.
func TestD17a_SchemaHasThirtySixTables(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			if got, want := len(s.Tables), 39; got != want {
				t.Fatalf("table count = %d, want %d", got, want)
			}
			present := map[string]bool{}
			for _, tbl := range s.Tables {
				present[tbl.Name] = true
			}
			for _, name := range []string{"users", "user_instance_tags"} {
				if !present[name] {
					t.Errorf("table %q missing from schema", name)
				}
			}
		})
	}
}

// TestD17a_UsersTableShape — users has 11 cols + single PK on id.
func TestD17a_UsersTableShape(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "users")
			if got, want := len(tbl.Columns), 11; got != want {
				t.Errorf("users column count = %d, want %d", got, want)
			}
			pk := tbl.PrimaryKey
			if pk == nil || len(pk.Parts) != 1 {
				gotN := 0
				if pk != nil {
					gotN = len(pk.Parts)
				}
				t.Fatalf("users PK parts = %d, want single-column PK on id", gotN)
			}
			if pk.Parts[0].C.Name != "id" {
				t.Errorf("users PK col = %q, want id", pk.Parts[0].C.Name)
			}
		})
	}
}

// TestD17a_UsersColumns — verify every column's name + nullability.
func TestD17a_UsersColumns(t *testing.T) {
	t.Parallel()
	tbl := mustTable(t, schema.Schema(schema.DialectPostgres), "users")
	type colSpec struct {
		name     string
		nullable bool
	}
	want := []colSpec{
		{"id", false},
		{"username", false},
		{"email", true},
		{"password_hash", true},
		{"oidc_subject", true},
		{"role", false},
		{"avatar_mode", false},
		{"preferred_language", true},
		{"created_at", false},
		{"updated_at", false},
		{"last_login_at", true},
	}
	for _, w := range want {
		col := findColumnInTable(tbl, w.name)
		if col == nil {
			t.Errorf("users.%s missing", w.name)
			continue
		}
		if col.Type.Null != w.nullable {
			t.Errorf("users.%s nullable = %v, want %v", w.name, col.Type.Null, w.nullable)
		}
	}
}

// TestD17a_UsersDefaults — role defaults to 'admin', avatar_mode to
// 'auto'.
func TestD17a_UsersDefaults(t *testing.T) {
	t.Parallel()
	tbl := mustTable(t, schema.Schema(schema.DialectPostgres), "users")
	cases := map[string]string{
		"role":        "'admin'",
		"avatar_mode": "'auto'",
	}
	for name, want := range cases {
		col := findColumnInTable(tbl, name)
		if col == nil {
			t.Errorf("users.%s missing", name)
			continue
		}
		lit, ok := col.Default.(*atlasschema.Literal)
		if !ok || lit.V != want {
			t.Errorf("users.%s default = %+v, want literal %q", name, col.Default, want)
		}
	}
}

// TestD17a_UsersUsernameUnique — UNIQUE on (username).
func TestD17a_UsersUsernameUnique(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "users")
			idx := mustIndex(t, tbl, "users_username_uniq")
			if !idx.Unique {
				t.Errorf("users_username_uniq should be UNIQUE")
			}
			if len(idx.Parts) != 1 || idx.Parts[0].C.Name != "username" {
				t.Errorf("users_username_uniq parts = %+v, want single column username", idx.Parts)
			}
		})
	}
}

// TestD17a_UsersOIDCSubjectPartialUnique — UNIQUE partial on
// (oidc_subject) WHERE oidc_subject IS NOT NULL.
func TestD17a_UsersOIDCSubjectPartialUnique(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "users")
			idx := mustIndex(t, tbl, "users_oidc_subject_uniq")
			if !idx.Unique {
				t.Errorf("users_oidc_subject_uniq should be UNIQUE")
			}
			if got := indexPredicate(d, idx); got != "oidc_subject IS NOT NULL" {
				t.Errorf("predicate = %q, want %q", got, "oidc_subject IS NOT NULL")
			}
		})
	}
}

// TestD17a_UsersCheckConstraints — CHECK on role + avatar_mode with
// the expected enum expressions.
func TestD17a_UsersCheckConstraints(t *testing.T) {
	t.Parallel()
	tbl := mustTable(t, schema.Schema(schema.DialectPostgres), "users")
	want := map[string]string{
		"users_role_check":        "role IN ('admin', 'user')",
		"users_avatar_mode_check": "avatar_mode IN ('auto', 'monogram', 'gravatar')",
	}
	got := map[string]string{}
	for _, a := range tbl.Attrs {
		if chk, ok := a.(*atlasschema.Check); ok {
			got[chk.Name] = chk.Expr
		}
	}
	for name, expr := range want {
		if gotExpr := got[name]; gotExpr != expr {
			t.Errorf("CHECK %s = %q, want %q", name, gotExpr, expr)
		}
	}
}

// TestD17a_UserInstanceTagsShape — 6 cols + composite PK ordered
// (user_id, instance_name).
func TestD17a_UserInstanceTagsShape(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "user_instance_tags")
			if got, want := len(tbl.Columns), 6; got != want {
				t.Errorf("user_instance_tags column count = %d, want %d", got, want)
			}
			pk := tbl.PrimaryKey
			if pk == nil || len(pk.Parts) != 2 {
				gotN := 0
				if pk != nil {
					gotN = len(pk.Parts)
				}
				t.Fatalf("user_instance_tags PK parts = %d, want 2", gotN)
			}
			if pk.Parts[0].C.Name != "user_id" || pk.Parts[1].C.Name != "instance_name" {
				t.Errorf("user_instance_tags PK ordering = (%s, %s), want (user_id, instance_name)",
					pk.Parts[0].C.Name, pk.Parts[1].C.Name)
			}
		})
	}
}

// TestD17a_UserInstanceTagsForeignKeys — 2 FKs, both CASCADE, correct
// parent tables.
func TestD17a_UserInstanceTagsForeignKeys(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "user_instance_tags")
			if len(tbl.ForeignKeys) != 2 {
				t.Fatalf("user_instance_tags FK count = %d, want 2", len(tbl.ForeignKeys))
			}
			for _, fk := range tbl.ForeignKeys {
				if fk.OnDelete != atlasschema.Cascade {
					t.Errorf("user_instance_tags FK %s OnDelete = %s, want CASCADE", fk.Symbol, fk.OnDelete)
				}
				switch fk.Symbol {
				case "user_instance_tags_user_id_fkey":
					if fk.RefTable == nil || fk.RefTable.Name != "users" {
						name := ""
						if fk.RefTable != nil {
							name = fk.RefTable.Name
						}
						t.Errorf("user_id FK ref = %s, want users", name)
					}
				case "user_instance_tags_instance_name_fkey":
					if fk.RefTable == nil || fk.RefTable.Name != "sonarr_instance" {
						name := ""
						if fk.RefTable != nil {
							name = fk.RefTable.Name
						}
						t.Errorf("instance_name FK ref = %s, want sonarr_instance", name)
					}
				default:
					t.Errorf("unexpected FK %s", fk.Symbol)
				}
			}
		})
	}
}

// TestD17a_UserInstanceTagsLabelUnique — UNIQUE composite-2 on
// (instance_name, sonarr_tag_label). Prevents two users claiming the
// same label on one instance.
func TestD17a_UserInstanceTagsLabelUnique(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			tbl := mustTable(t, schema.Schema(d), "user_instance_tags")
			idx := mustIndex(t, tbl, "user_instance_tags_label")
			if !idx.Unique {
				t.Errorf("user_instance_tags_label should be UNIQUE")
			}
			if len(idx.Parts) != 2 ||
				idx.Parts[0].C.Name != "instance_name" ||
				idx.Parts[1].C.Name != "sonarr_tag_label" {
				t.Errorf("user_instance_tags_label parts = %+v, want (instance_name, sonarr_tag_label)", idx.Parts)
			}
		})
	}
}

// findColumnInTable returns the named column from tbl, or nil if
// absent. Local to D-1-7a unit tests to keep the helper colocated.
func findColumnInTable(tbl *atlasschema.Table, name string) *atlasschema.Column {
	for _, c := range tbl.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}
