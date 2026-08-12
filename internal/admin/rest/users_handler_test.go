package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// fakeUsersStore satisfies both authapp.UsersRepository (for the real usecase)
// and the handler's CallerDirectory (GetByUsername).
type fakeUsersStore struct {
	mu     sync.Mutex
	byID   map[uint]admin.User
	byName map[string]uint
}

func newFakeUsersStore(seed ...admin.User) *fakeUsersStore {
	s := &fakeUsersStore{byID: map[uint]admin.User{}, byName: map[string]uint{}}
	for _, u := range seed {
		s.byID[u.ID] = u
		if u.Username != "" {
			s.byName[u.Username] = u.ID
		}
	}
	return s
}

func (s *fakeUsersStore) List(_ context.Context) ([]admin.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]admin.User, 0, len(s.byID))
	for _, u := range s.byID {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *fakeUsersStore) GetByID(_ context.Context, id uint) (admin.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok {
		return admin.User{}, ports.ErrNotFound
	}
	return u, nil
}

func (s *fakeUsersStore) GetByUsername(_ context.Context, name string) (admin.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byName[name]
	if !ok {
		return admin.User{}, ports.ErrNotFound
	}
	return s.byID[id], nil
}

func (s *fakeUsersStore) CountAdmins(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, u := range s.byID {
		if u.Role == admin.RoleAdmin {
			n++
		}
	}
	return n, nil
}

func (s *fakeUsersStore) UpdateRole(_ context.Context, id uint, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok {
		return ports.ErrNotFound
	}
	u.Role = role
	s.byID[id] = u
	return nil
}

func (s *fakeUsersStore) UpdatePermissions(_ context.Context, id uint, patch ports.UserPermissionsPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byID[id]
	if !ok {
		return ports.ErrNotFound
	}
	if patch.AutoApprove != nil {
		u.AutoApprove = *patch.AutoApprove
	}
	if patch.Request != nil {
		u.Request = *patch.Request
	}
	if patch.ManageRequests != nil {
		u.ManageRequests = *patch.ManageRequests
	}
	if patch.ManageUsers != nil {
		u.ManageUsers = *patch.ManageUsers
	}
	if patch.Request4K != nil {
		u.Request4K = *patch.Request4K
	}
	s.byID[id] = u
	return nil
}

func (s *fakeUsersStore) Delete(_ context.Context, id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return ports.ErrNotFound
	}
	delete(s.byID, id)
	return nil
}

func (s *fakeUsersStore) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func setupUsers(store *fakeUsersStore, callerUsername string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	uc := authapp.NewUsersUseCase(store)
	h := NewUsersHandler(uc, store, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if callerUsername != "" {
			c.Set(middleware.UsernameContextKey, callerUsername)
		}
		c.Next()
	})
	r.GET("/api/v1/admin/users", h.List)
	r.PATCH("/api/v1/admin/users/:id", h.Patch)
	r.DELETE("/api/v1/admin/users/:id", h.Delete)
	return r
}

func adminRow(id uint, name string) admin.User {
	return admin.User{ID: id, Username: name, Role: admin.RoleAdmin, ManageUsers: true}
}
func userRow(id uint, name string) admin.User {
	return admin.User{ID: id, Username: name, Role: admin.RoleUser, Request: true}
}

func do(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUsersHandler_List_200_NoPasswordHash(t *testing.T) {
	t.Parallel()
	pw := "secret-hash"
	store := newFakeUsersStore(
		admin.User{ID: 1, Username: "admin", Role: admin.RoleAdmin, PasswordHash: pw, ManageUsers: true},
		userRow(2, "bob"),
	)
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodGet, "/api/v1/admin/users", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "password_hash")
	assert.NotContains(t, w.Body.String(), pw)

	var resp userListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	assert.Equal(t, "admin", resp.Items[0].Username)
	assert.True(t, resp.Items[1].Permissions.Request)
}

func TestUsersHandler_Patch_200_PromoteUser(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"), userRow(2, "bob"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/2", `{"role":"admin","request_4k":true}`)
	require.Equal(t, http.StatusOK, w.Code)
	var item userItem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &item))
	assert.Equal(t, admin.RoleAdmin, item.Role)
	assert.True(t, item.Permissions.Request4K)
}

func TestUsersHandler_Patch_400_InvalidRole(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"), userRow(2, "bob"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/2", `{"role":"root"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_ROLE")
}

func TestUsersHandler_Patch_400_MalformedBody(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"), userRow(2, "bob"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/2", `{"role":`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUsersHandler_Patch_400_UnknownField(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"), userRow(2, "bob"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/2", `{"is_superuser":true}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUsersHandler_Patch_400_BadID(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/abc", `{"role":"user"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUsersHandler_Patch_404_NotFound(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/99", `{"role":"user"}`)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "NOT_FOUND")
}

func TestUsersHandler_Patch_409_SelfLockout(t *testing.T) {
	t.Parallel()
	// Two admins so it is not last-admin — the reject is self-lockout.
	store := newFakeUsersStore(adminRow(1, "admin"), adminRow(3, "admin2"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/1", `{"role":"user"}`)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "SELF_LOCKOUT")
}

func TestUsersHandler_Patch_409_LastAdmin(t *testing.T) {
	t.Parallel()
	// api-key caller demoting the sole admin — self-lockout skipped, last-admin fires.
	store := newFakeUsersStore(adminRow(5, "admin"))
	r := setupUsers(store, "api-key")
	w := do(t, r, http.MethodPatch, "/api/v1/admin/users/5", `{"role":"user"}`)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "LAST_ADMIN")
}

func TestUsersHandler_Delete_204(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"), userRow(2, "bob"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodDelete, "/api/v1/admin/users/2", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	_, err := store.GetByID(context.Background(), 2)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestUsersHandler_Delete_409_Self(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"), adminRow(3, "admin2"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodDelete, "/api/v1/admin/users/1", "")
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "SELF_LOCKOUT")
}

func TestUsersHandler_Delete_409_LastAdmin(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(5, "admin"))
	r := setupUsers(store, "api-key")
	w := do(t, r, http.MethodDelete, "/api/v1/admin/users/5", "")
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "LAST_ADMIN")
}

func TestUsersHandler_Delete_404(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodDelete, "/api/v1/admin/users/99", "")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestUsersHandler_Delete_400_BadID(t *testing.T) {
	t.Parallel()
	store := newFakeUsersStore(adminRow(1, "admin"))
	r := setupUsers(store, "admin")
	w := do(t, r, http.MethodDelete, "/api/v1/admin/users/xyz", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
}
