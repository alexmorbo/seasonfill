package externalservices

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// In-process HTTP proxy: forwards every request straight through.
// Good enough to exercise http.Transport.Proxy dispatch end-to-end
// without external dependencies.
func newEchoProxy(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Proxy", "echo")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHttpClientFor_NoProxy(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "upstream-ok")
	}))
	t.Cleanup(upstream.Close)

	c, err := HttpClientFor(Settings{})
	if err != nil {
		t.Fatalf("HttpClientFor: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestHttpClientFor_HTTPProxy(t *testing.T) {
	t.Parallel()
	proxy := newEchoProxy(t)
	c, err := HttpClientFor(Settings{ProxyURL: proxy.URL})
	if err != nil {
		t.Fatalf("HttpClientFor: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://upstream.invalid/whatever", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("get via proxy: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Proxy"); got != "echo" {
		t.Fatalf("proxy header missing: %q", got)
	}
}

func TestHttpClientFor_SOCKS5(t *testing.T) {
	t.Parallel()
	c, err := HttpClientFor(Settings{ProxyURL: "socks5://127.0.0.1:1", ProxyUsername: "u", ProxyPassword: "p"})
	if err != nil {
		t.Fatalf("HttpClientFor socks5: %v", err)
	}
	tr := c.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatalf("socks5 transport must clear Proxy")
	}
	if tr.DialContext == nil {
		t.Fatalf("socks5 transport must wire DialContext")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tr.DialContext(ctx, "tcp", "example.com:80")
	if err == nil {
		t.Fatalf("expected dial error after cancel")
	}
}

func TestHttpClientFor_SOCKS4(t *testing.T) {
	t.Parallel()
	// golang.org/x/net/proxy.FromURL only natively supports socks5;
	// socks4 is accepted into the switch arm but the dialer
	// construction returns an "unknown scheme" error wrapped in
	// ErrProxyConfig. Asserting the documented behaviour here so a
	// future native socks4 dialer addition flips this test red on the
	// success path rather than silently passing.
	_, err := HttpClientFor(Settings{ProxyURL: "socks4://127.0.0.1:1"})
	if err == nil {
		t.Fatalf("expected socks4 error pending native dialer support")
	}
	if !errors.Is(err, ErrProxyConfig) {
		t.Fatalf("socks4 dialer error must wrap ErrProxyConfig, got %v", err)
	}
}

func TestHttpClientFor_InvalidScheme(t *testing.T) {
	t.Parallel()
	_, err := HttpClientFor(Settings{ProxyURL: "ftp://example.com"})
	if err == nil || !strings.Contains(err.Error(), "unsupported scheme") {
		t.Fatalf("expected unsupported scheme err, got %v", err)
	}
}

func TestHttpClientFor_BadURL(t *testing.T) {
	t.Parallel()
	_, err := HttpClientFor(Settings{ProxyURL: "://bad"})
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestMerge_EnvOverridesDB(t *testing.T) {
	t.Parallel()
	db := Settings{APIKey: "from_db", APIKeyLast4: "m_db", ProxyURL: "http://db:1"}
	env := func(name string) string {
		switch name {
		case "SEASONFILL_TMDB_TOKEN":
			return "from_env_token"
		case "SEASONFILL_TMDB_PROXY_USER":
			return "alice"
		}
		return ""
	}
	out := Merge(ServiceTMDB, db, env)
	if out.APIKey != "from_env_token" {
		t.Fatalf("env token must win: %q", out.APIKey)
	}
	if out.APIKeyLast4 != "oken" {
		t.Fatalf("last4 must rebuild from env: %q", out.APIKeyLast4)
	}
	if out.ProxyURL != "http://db:1" {
		t.Fatalf("db proxy_url must persist when env unset: %q", out.ProxyURL)
	}
	if out.ProxyUsername != "alice" {
		t.Fatalf("env proxy user must win: %q", out.ProxyUsername)
	}
	if !out.Enabled {
		t.Fatalf("env token must enable the service")
	}
}

func TestSettings_StringRedacted(t *testing.T) {
	t.Parallel()
	s := Settings{
		Service:       ServiceTMDB,
		APIKey:        "supersecret",
		APIKeyLast4:   "cret",
		ProxyURL:      "http://user:pass@proxy.example.com:8080/path",
		ProxyPassword: "pass",
	}
	str := s.String()
	if strings.Contains(str, "supersecret") || strings.Contains(str, "pass") || strings.Contains(str, "user:") {
		t.Fatalf("redacted formatter leaked secrets: %s", str)
	}
	if !strings.Contains(str, "proxy.example.com:8080") {
		t.Fatalf("redacted formatter must keep host: %s", str)
	}
}
