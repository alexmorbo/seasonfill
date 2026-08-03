package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

func newMetadataRouter(t *testing.T, h *InstanceProbeHandler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/admin/instances/metadata", h.Metadata)
	return r
}

func doMetadata(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/admin/instances/metadata", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// fakeSonarrMetadata serves the two endpoints the metadata probe hits and
// asserts every request carries the expected X-Api-Key.
func fakeSonarrMetadata(t *testing.T, wantKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, wantKey, r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/qualityprofile":
			_, _ = io.WriteString(w, `[{"id":1,"name":"HD-1080p"}]`)
		case "/api/v3/rootfolder":
			_, _ = io.WriteString(w, `[{"id":1,"path":"/tv","accessible":true,"freeSpace":123}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestMetadata_StoredKeyFallback: edit mode — blank api_key + name populates
// the pickers using the stored decrypted key.
func TestMetadata_StoredKeyFallback(t *testing.T) {
	t.Parallel()
	srv := fakeSonarrMetadata(t, "STORED")
	t.Cleanup(srv.Close)

	h := NewInstanceProbeHandler(nil, nil,
		WithStoredKeyLookup(func(_ context.Context, name string) (string, error) {
			assert.Equal(t, "homelab", name)
			return "STORED", nil
		}))
	r := newMetadataRouter(t, h)

	name := "homelab"
	w := doMetadata(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "", Name: &name})
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got dto.InstanceMetadataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.QualityProfiles, 1)
	assert.Equal(t, "HD-1080p", got.QualityProfiles[0].Name)
	require.Len(t, got.RootFolders, 1)
	assert.Equal(t, "/tv", got.RootFolders[0].Path)
	assert.True(t, got.RootFolders[0].Accessible)
}

// TestMetadata_TypedKeyOverridesStored: a non-empty api_key wins; lookup unused.
func TestMetadata_TypedKeyOverridesStored(t *testing.T) {
	t.Parallel()
	srv := fakeSonarrMetadata(t, "TYPED")
	t.Cleanup(srv.Close)

	h := NewInstanceProbeHandler(nil, nil,
		WithStoredKeyLookup(func(_ context.Context, _ string) (string, error) {
			t.Error("stored-key lookup must NOT run when api_key is provided")
			return "STORED", nil
		}))
	r := newMetadataRouter(t, h)

	name := "homelab"
	w := doMetadata(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "TYPED", Name: &name})
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got dto.InstanceMetadataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got.QualityProfiles, 1)
}

// TestMetadata_StoredKeyNotFound: name but no stored key → 404.
func TestMetadata_StoredKeyNotFound(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(nil, nil,
		WithStoredKeyLookup(func(_ context.Context, _ string) (string, error) {
			return "", ErrStoredKeyUnavailable
		}))
	r := newMetadataRouter(t, h)

	name := "ghost"
	w := doMetadata(t, r, dto.InstanceTestRequest{URL: "http://sonarr:8989", APIKey: "", Name: &name})
	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "STORED_KEY_NOT_FOUND")
}
