package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/instance"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// crudFakeRepo is a minimal in-process implementation of
// ports.SonarrInstanceRepository. We can't reuse the one in
// application/instance/usecase_test.go (different package) so this
// is a deliberate duplication kept tight to handler-test needs.
type crudFakeRepo struct {
	rows    map[string]runtime.InstanceSnapshot
	updated map[string]time.Time
	count   int
	nextID  uint
}

func newCRUDFakeRepo() *crudFakeRepo {
	return &crudFakeRepo{
		rows: map[string]runtime.InstanceSnapshot{},
		updated: map[string]time.Time{}, nextID: 1,
	}
}
func (f *crudFakeRepo) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	out := make([]runtime.InstanceSnapshot, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}
func (f *crudFakeRepo) GetByName(_ context.Context, name string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	r, ok := f.rows[name]
	if !ok {
		return runtime.InstanceSnapshot{}, ports.ErrNotFound
	}
	return r, nil
}
func (f *crudFakeRepo) Create(_ context.Context, inst runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	inst.ID = f.nextID
	f.nextID++
	f.rows[inst.Name] = inst
	f.updated[inst.Name] = time.Now().UTC()
	f.count++
	return inst.ID, nil
}
func (f *crudFakeRepo) Update(_ context.Context, inst runtime.InstanceSnapshot, _ *crypto.Cipher) error {
	f.rows[inst.Name] = inst
	f.updated[inst.Name] = time.Now().UTC()
	return nil
}
func (f *crudFakeRepo) UpdateWithOptions(ctx context.Context, inst runtime.InstanceSnapshot, c *crypto.Cipher, _ bool, ifUnmodifiedSince *time.Time) error {
	if ifUnmodifiedSince != nil {
		stored, ok := f.updated[inst.Name]
		if ok {
			s := stored.Truncate(time.Second)
			p := ifUnmodifiedSince.Truncate(time.Second)
			if s.After(p) {
				return ports.ErrStaleWrite
			}
		}
	}
	return f.Update(ctx, inst, c)
}
func (f *crudFakeRepo) Delete(_ context.Context, name string) error {
	if _, ok := f.rows[name]; !ok {
		return ports.ErrNotFound
	}
	delete(f.rows, name)
	delete(f.updated, name)
	f.count--
	return nil
}
func (f *crudFakeRepo) Count(_ context.Context) (int, error)     { return f.count, nil }
func (f *crudFakeRepo) GetUpdatedAt(_ context.Context, name string) (time.Time, error) {
	ts, ok := f.updated[name]
	if !ok {
		return time.Time{}, ports.ErrNotFound
	}
	return ts, nil
}

type crudFakeRuntime struct{}

func (crudFakeRuntime) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	return ports.RuntimeConfigRow{}, nil
}
func (crudFakeRuntime) Upsert(_ context.Context, _ runtime.Snapshot, _ *time.Time) error {
	return nil
}
func (crudFakeRuntime) SaveAPIKey(_ context.Context, _ []byte, _ bool) error { return nil }

func setupCRUD(t *testing.T) (*gin.Engine, *crudFakeRepo) {
	t.Helper()
	repo := newCRUDFakeRepo()
	uc := instance.New(repo, crudFakeRuntime{}, nil, runtime.NewBus(nil), slog.Default())
	h := NewInstanceCRUDHandler(uc, slog.Default())
	r := gin.New()
	r.GET("/api/v1/instances/:name", h.Get)
	r.POST("/api/v1/instances", h.Create)
	r.PUT("/api/v1/instances/:name", h.Update)
	r.DELETE("/api/v1/instances/:name", h.Delete)
	return r, repo
}

func doJSON(t *testing.T, r *gin.Engine, method, url string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func createBody(name string) map[string]any {
	return map[string]any{"name": name, "url": "http://sonarr:8989", "api_key": "abc"}
}

func TestCRUD_Create_201_PublishesAndMasks(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "***", body["api_key"])
	assert.NotEmpty(t, w.Header().Get("Last-Modified"))
}

func TestCRUD_Create_DuplicateName_409(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "DUPLICATE_NAME")
}

func TestCRUD_Create_MissingField_400(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	w := doJSON(t, r, http.MethodPost, "/api/v1/instances",
		map[string]any{"name": "alpha", "url": "http://x"}, nil) // no api_key
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCRUD_Get_MasksKeyAndSetsLastModified(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodGet, "/api/v1/instances/alpha", nil, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "***", body["api_key"])
	lm := w.Header().Get("Last-Modified")
	require.NotEmpty(t, lm)
	_, err := http.ParseTime(lm)
	assert.NoError(t, err, "Last-Modified must parse as RFC1123: %q", lm)
}

func TestCRUD_Put_NameImmutable_400(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", createBody("beta"), nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "NAME_IMMUTABLE")
}

func TestCRUD_Put_EmptyKey_Preserves(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	body := createBody("alpha")
	body["api_key"] = ""
	body["url"] = "http://updated:8989"
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", body, nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "http://updated:8989", got["url"])
	assert.Equal(t, "***", got["api_key"])
}

func TestCRUD_Put_Stale_IfUnmodifiedSince_412(t *testing.T) {
	t.Parallel()
	r, repo := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	// Force the stored timestamp into the future so any IUS in the past
	// is stale by construction.
	repo.updated["alpha"] = time.Now().UTC().Add(time.Hour)
	body := createBody("alpha")
	w := doJSON(t, r, http.MethodPut, "/api/v1/instances/alpha", body,
		map[string]string{"If-Unmodified-Since": time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)})
	assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	assert.Contains(t, w.Body.String(), "STALE_WRITE")
}

func TestCRUD_Delete_Last_409(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	w := doJSON(t, r, http.MethodDelete, "/api/v1/instances/alpha", nil, nil)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "LAST_INSTANCE")
}

func TestCRUD_Delete_NonLast_204(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("alpha"), nil)
	doJSON(t, r, http.MethodPost, "/api/v1/instances", createBody("beta"), nil)
	w := doJSON(t, r, http.MethodDelete, "/api/v1/instances/alpha", nil, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCRUD_MalformedBody_400(t *testing.T) {
	t.Parallel()
	r, _ := setupCRUD(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
