package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/netguard"
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

// hardenedClient mirrors the production wiring from cmd/server/main.go.
// Tests that exercise the SSRF guards MUST use it; tests that only need
// loopback to a httptest.Server use srv.Client() because the guard would
// (correctly) reject 127.0.0.1.
func hardenedClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Control: netguard.BlockPrivate,
				Timeout: 500 * time.Millisecond,
			}).DialContext,
		},
		Timeout: 2 * time.Second,
	}
}

func TestProbe_RedirectRejected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://example.invalid/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	// Use srv.Client() but layer CheckRedirect on top — we want to exercise
	// the handler branch, not the dialer guard.
	c := srv.Client()
	c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	h := NewInstanceProbeHandler(c, nil)
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "x"})
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got dto.InstanceTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.OK)
	assert.Equal(t, "redirect rejected", got.Reason)
}

func TestProbe_PrivateHostRejected(t *testing.T) {
	t.Parallel()
	cases := []string{
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://127.0.0.1/",
		"http://169.254.169.254/",
		"http://[::1]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
	}
	h := NewInstanceProbeHandler(hardenedClient(t), nil)
	r := newProbeRouter(t, h)

	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			w := doProbe(t, r, dto.InstanceTestRequest{URL: raw, APIKey: "x"})
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s url=%s", w.Body.String(), raw)
			assert.Contains(t, w.Body.String(), "INVALID_HOST")
		})
	}
}

// TestProbe_DNSRebindRejected — hostname that resolves to a private IP.
// localhost is the simplest portable case: it resolves to 127.0.0.1 / ::1.
// The dialer Control hook MUST trigger AFTER resolution, so this exercises
// the rebinding-defeat path.
func TestProbe_DNSRebindRejected(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(hardenedClient(t), nil)
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{
		URL:    "http://localhost:1/",
		APIKey: "x",
	})
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "INVALID_HOST")
}

func TestProbe_NonJSONContentType(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html>not sonarr</html>")
	}))
	t.Cleanup(srv.Close)

	h := NewInstanceProbeHandler(srv.Client(), nil)
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "x"})
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got dto.InstanceTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.OK)
	assert.Equal(t, "not a Sonarr API endpoint", got.Reason)
}

// Compile-time guard: keep the syscall import live; the BlockPrivate
// signature relies on syscall.RawConn elsewhere in the tree.
var _ = func() syscall.RawConn { return nil }

// Catch a regression where ErrBlockedHost stops being errors.Is-detectable.
func TestProbe_BlockedHostErrorChainStable(t *testing.T) {
	t.Parallel()
	d := &net.Dialer{Control: netguard.BlockPrivate, Timeout: 200 * time.Millisecond}
	_, err := d.DialContext(t.Context(), "tcp", "127.0.0.1:1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, netguard.ErrBlockedHost),
		"errors.Is must walk the net.OpError chain")
}

// --- Part 028i: uncovered branches ---

// TestProbe_ReasonForStatus_Default exercises the default branch of
// reasonForStatus directly. The default fires for status codes that
// are not 401/403, not 4xx, and not 5xx (e.g., 1xx codes that cannot
// be delivered by net/http but can be passed to the function directly).
// The test is in the same package (handlers) so it can call the unexported
// reasonForStatus function.
func TestProbe_ReasonForStatus_Default(t *testing.T) {
	t.Parallel()
	// Status 199 is <200, not 4xx, not 5xx → hits default branch.
	got := reasonForStatus(199)
	assert.Contains(t, got, "unexpected status")
	assert.Contains(t, got, "199")
	// Status 299 is >=200 and <300 but not currently reachable in the handler;
	// verify it also maps to default just to document the invariant.
	got299 := reasonForStatus(299)
	assert.Contains(t, got299, "unexpected status")
}

// TestProbe_ValidateURL_ParseError exercises the url.Parse error branch.
// A URL containing a control character (0x7f DEL) forces url.Parse to fail.
func TestProbe_ValidateURL_ParseError(t *testing.T) {
	t.Parallel()
	h := NewInstanceProbeHandler(nil, nil)
	r := newProbeRouter(t, h)
	// ASCII DEL character inside the URL forces url.Parse to return an error.
	w := doProbe(t, r, dto.InstanceTestRequest{URL: "http://foo\x7f.example/", APIKey: "x"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}

func TestProbe_AllowPrivate_PermitsAndRevokesLoopback(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"4.0.0.999"}`)
	}))
	t.Cleanup(srv.Close)

	var allow atomic.Bool
	allow.Store(true)
	guard := netguard.Guard{AllowPrivate: allow.Load}

	c := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: (&net.Dialer{
				Control: guard.Control,
				Timeout: 500 * time.Millisecond,
			}).DialContext,
		},
		Timeout: 2 * time.Second,
	}
	h := NewInstanceProbeHandler(c, nil)
	r := newProbeRouter(t, h)

	w := doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "x"})
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got dto.InstanceTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.True(t, got.OK, "homelab toggle must let loopback through: %s", w.Body.String())
	assert.Equal(t, "4.0.0.999", got.Version)

	// Flip the atomic — same client, same target → must now block.
	allow.Store(false)
	w = doProbe(t, r, dto.InstanceTestRequest{URL: srv.URL, APIKey: "x"})
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "INVALID_HOST")
}
