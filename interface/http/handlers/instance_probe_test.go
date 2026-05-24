package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

func newProbeRouter(t *testing.T, h *InstanceProbeHandler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/instances/test", h.Test)
	return r
}

func doProbe(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances/test", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestProbe_Happy(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/api/v3/system/status", req.URL.Path)
		assert.Equal(t, "secret", req.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"4.0.0.999"}`)
	}))
	t.Cleanup(srv.Close)

	h := NewInstanceProbeHandler(srv.Client(), nil)
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "secret"})
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got dto.InstanceTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.True(t, got.OK)
	assert.Equal(t, "4.0.0.999", got.Version)
	assert.Empty(t, got.Reason)
}

func TestProbe_AuthFailed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	h := NewInstanceProbeHandler(srv.Client(), nil)
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "wrong"})
	require.Equal(t, http.StatusOK, w.Code)

	var got dto.InstanceTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.OK)
	assert.Equal(t, "authentication failed", got.Reason)
}

func TestProbe_UpstreamError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	h := NewInstanceProbeHandler(srv.Client(), nil)
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "x"})
	require.Equal(t, http.StatusOK, w.Code)

	var got dto.InstanceTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.OK)
	assert.Equal(t, "upstream error", got.Reason)
}

func TestProbe_Timeout(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	// close(block) must run before srv.Close so the blocked handler can
	// return and the server can shut down cleanly. Cleanups run LIFO, so
	// register srv.Close first, then close(block).
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	h := NewInstanceProbeHandler(srv.Client(), nil, WithProbeTimeout(50*time.Millisecond))
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "x"})
	require.Equal(t, http.StatusGatewayTimeout, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "PROBE_TIMEOUT")
}

func TestProbe_BadScheme(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(nil, nil)
	r := newProbeRouter(t, h)
	w := doProbe(t, r, dto.InstanceTestRequest{URL: "ftp://example", APIKey: "x"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
	assert.Contains(t, w.Body.String(), "scheme")
}

func TestProbe_MissingURL(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(nil, nil)
	r := newProbeRouter(t, h)
	w := doProbe(t, r, dto.InstanceTestRequest{URL: "", APIKey: "x"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "url is required")
}

func TestProbe_MissingAPIKey(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(nil, nil)
	r := newProbeRouter(t, h)
	w := doProbe(t, r, dto.InstanceTestRequest{URL: "http://x", APIKey: ""})
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "api_key is required")
}

func TestProbe_MalformedBody(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(nil, nil)
	r := newProbeRouter(t, h)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances/test", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "malformed body")
}

func TestProbe_WrongContentType(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(nil, nil)
	r := newProbeRouter(t, h)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/instances/test", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
