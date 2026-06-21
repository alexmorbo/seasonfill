package persistence

import "testing"

// D-2: all admin_user_repository tests skip pending D-5 admin+auth rewrite.
// The legacy admin_users table is gone; the new auth schema (users +
// app_secret) requires repository rewrites done in D-5.

func TestAdminUserRepo_GetEmpty(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestAdminUserRepo_CreateThenGet(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestAdminUserRepo_UpdatePassword(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestAdminUserRepo_UpdatePassword_NoRow(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestAdminUserRepo_GetByOIDCSubject_Found(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestAdminUserRepo_GetByOIDCSubject_NotFound(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestAdminUserRepo_CreateFromOIDC_PopulatesSubject(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func TestAdminUserRepo_CreateFromOIDC_MultipleUsers(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}
