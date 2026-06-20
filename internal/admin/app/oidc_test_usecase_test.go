package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeOIDCProvider builds a minimal httptest server that looks like an OIDC provider.
// It serves /.well-known/openid-configuration, /jwks, and /token.
func makeOIDCProvider(t *testing.T, jwksKeys int) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			base := srv.URL
			doc := map[string]string{
				"issuer":                 base,
				"authorization_endpoint": base + "/auth",
				"token_endpoint":         base + "/token",
				"jwks_uri":               base + "/jwks",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		case "/jwks":
			keys := make([]map[string]string, jwksKeys)
			for i := range keys {
				keys[i] = map[string]string{"kty": "RSA", "kid": "k1"}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		case "/token":
			w.WriteHeader(http.StatusMethodNotAllowed) // HEAD → 405 = reachable
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOIDCTestUseCase_HappyPath(t *testing.T) {
	t.Parallel()
	srv := makeOIDCProvider(t, 1)
	uc := NewOIDCTestUseCase()
	result := uc.Test(context.Background(), OIDCTestInput{Issuer: srv.URL})

	assert.True(t, result.Discovery.OK, "discovery must succeed")
	assert.True(t, result.IssuerMatch.OK, "issuer must match")
	assert.True(t, result.JWKS.OK, "JWKS must succeed")
	assert.Equal(t, 1, result.JWKS.Keys)
	assert.True(t, result.TokenEndpoint.OK, "token endpoint must be reachable")
}

func TestOIDCTestUseCase_EmptyIssuer(t *testing.T) {
	t.Parallel()
	uc := NewOIDCTestUseCase()
	result := uc.Test(context.Background(), OIDCTestInput{Issuer: ""})
	assert.False(t, result.Discovery.OK)
	assert.Equal(t, "issuer is empty", result.Discovery.Error)
	assert.False(t, result.IssuerMatch.OK)
	assert.False(t, result.JWKS.OK)
	assert.False(t, result.TokenEndpoint.OK)
}

func TestOIDCTestUseCase_DiscoveryNotFound(t *testing.T) {
	t.Parallel()
	// A server that returns 404 for discovery.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	uc := NewOIDCTestUseCase()
	result := uc.Test(context.Background(), OIDCTestInput{Issuer: srv.URL})
	assert.False(t, result.Discovery.OK)
	assert.NotEmpty(t, result.Discovery.Error)
	assert.False(t, result.IssuerMatch.OK)
	assert.False(t, result.JWKS.OK)
	assert.False(t, result.TokenEndpoint.OK)
}

func TestOIDCTestUseCase_JWKSEmptyKeys(t *testing.T) {
	t.Parallel()
	srv := makeOIDCProvider(t, 0) // 0 keys → JWKS ok=false
	uc := NewOIDCTestUseCase()
	result := uc.Test(context.Background(), OIDCTestInput{Issuer: srv.URL})
	assert.True(t, result.Discovery.OK)
	assert.False(t, result.JWKS.OK)
	assert.Equal(t, 0, result.JWKS.Keys)
}

func TestOIDCTestUseCase_Timeout(t *testing.T) {
	t.Parallel()
	// A server that sleeps longer than the client timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Second)
	}))
	t.Cleanup(srv.Close)
	uc := &OIDCTestUseCase{client: &http.Client{Timeout: 100 * time.Millisecond}}
	start := time.Now()
	result := uc.Test(context.Background(), OIDCTestInput{Issuer: srv.URL})
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 2*time.Second, "should not wait for full server sleep")
	assert.False(t, result.Discovery.OK)
	assert.NotEmpty(t, result.Discovery.Error)
}

func TestDerivedRedirectURL_ExplicitWins(t *testing.T) {
	t.Parallel()
	cfg := OIDCConfig{RedirectURL: "https://explicit.example.com/cb"}
	info := RequestInfo{Host: "other.example.com", XFH: "proxy.example.com"}
	assert.Equal(t, "https://explicit.example.com/cb", DerivedRedirectURL(cfg, info))
}

func TestDerivedRedirectURL_XFHWinsOverHost(t *testing.T) {
	t.Parallel()
	cfg := OIDCConfig{}
	info := RequestInfo{Host: "internal.host", XFH: "public.example.com", XFP: "https"}
	result := DerivedRedirectURL(cfg, info)
	assert.Equal(t, "https://public.example.com/api/v1/auth/oidc/callback", result)
}

func TestDerivedRedirectURL_IgnoresXFPDowngrade(t *testing.T) {
	t.Parallel()
	// Multi-proxy setups frequently downgrade XFP to http between the TLS-
	// terminating front proxy and the ingress controller. We force https
	// for non-loopback hosts regardless of XFP.
	cfg := OIDCConfig{}
	info := RequestInfo{Host: "app.example.com", XFP: "http"}
	result := DerivedRedirectURL(cfg, info)
	assert.Equal(t, "https://app.example.com/api/v1/auth/oidc/callback", result)
}

func TestDerivedRedirectURL_LoopbackUsesHTTP(t *testing.T) {
	t.Parallel()
	cfg := OIDCConfig{}
	for _, host := range []string{"localhost", "localhost:5173", "127.0.0.1", "127.0.0.1:8080"} {
		info := RequestInfo{Host: host}
		result := DerivedRedirectURL(cfg, info)
		assert.True(t, strings.HasPrefix(result, "http://"), "host=%s got=%s", host, result)
	}
}

func TestDerivedRedirectURL_DefaultsToHTTPS(t *testing.T) {
	t.Parallel()
	cfg := OIDCConfig{}
	info := RequestInfo{Host: "app.example.com"}
	result := DerivedRedirectURL(cfg, info)
	assert.Equal(t, "https://app.example.com/api/v1/auth/oidc/callback", result)
}

func TestDerivedRedirectURL_NoHostReturnsEmpty(t *testing.T) {
	t.Parallel()
	cfg := OIDCConfig{}
	info := RequestInfo{}
	require.Empty(t, DerivedRedirectURL(cfg, info))
}
