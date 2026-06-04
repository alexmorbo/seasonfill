package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OIDCTestInput carries the draft values the operator wants to test.
// Empty fields fall back to the snapshot values in the handler.
type OIDCTestInput struct {
	Issuer   string
	ClientID string
	Scopes   []string
}

// OIDCTestResult is the response shape for POST /auth/oidc/test.
type OIDCTestResult struct {
	Discovery     CheckDiscovery     `json:"discovery"`
	IssuerMatch   CheckIssuerMatch   `json:"issuer_match"`
	JWKS          CheckJWKS          `json:"jwks"`
	TokenEndpoint CheckTokenEndpoint `json:"token_endpoint"`
}

// CheckDiscovery reports the result of fetching /.well-known/openid-configuration.
type CheckDiscovery struct {
	OK               bool   `json:"ok"`
	Error            string `json:"error,omitempty"`
	DiscoveredIssuer string `json:"discovered_issuer,omitempty"`
}

// CheckIssuerMatch reports whether the discovered issuer matches the configured one.
type CheckIssuerMatch struct {
	OK       bool   `json:"ok"`
	Expected string `json:"expected"`
	Got      string `json:"got"`
}

// CheckJWKS reports the result of fetching the JWKS endpoint.
type CheckJWKS struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Keys  int    `json:"keys"`
}

// CheckTokenEndpoint reports reachability of the token endpoint.
type CheckTokenEndpoint struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	URL   string `json:"url"`
}

// oidcDiscoveryDoc is the minimal subset of an OpenID Connect discovery document.
type oidcDiscoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// OIDCTestUseCase performs reachability checks against an OIDC provider.
type OIDCTestUseCase struct {
	client *http.Client
}

// NewOIDCTestUseCase creates an OIDCTestUseCase with a 10s HTTP timeout.
func NewOIDCTestUseCase() *OIDCTestUseCase {
	return &OIDCTestUseCase{client: &http.Client{Timeout: 10 * time.Second}}
}

// Test runs the four reachability sub-checks against the provider.
func (uc *OIDCTestUseCase) Test(ctx context.Context, in OIDCTestInput) OIDCTestResult {
	var result OIDCTestResult

	if strings.TrimSpace(in.Issuer) == "" {
		result.Discovery.Error = "issuer is empty"
		return result
	}

	doc, err := uc.discover(ctx, in.Issuer)
	if err != nil {
		result.Discovery.Error = err.Error()
		return result
	}
	result.Discovery.OK = true
	result.Discovery.DiscoveredIssuer = doc.Issuer

	// Issuer match (trailing-slash normalised).
	result.IssuerMatch.Expected = in.Issuer
	result.IssuerMatch.Got = doc.Issuer
	result.IssuerMatch.OK = strings.TrimRight(doc.Issuer, "/") == strings.TrimRight(in.Issuer, "/")

	// JWKS check.
	result.JWKS = uc.checkJWKS(ctx, doc.JWKSURI)

	// Token endpoint reachability.
	result.TokenEndpoint = uc.checkTokenEndpoint(ctx, doc.TokenEndpoint)

	return result
}

func (uc *OIDCTestUseCase) discover(ctx context.Context, issuer string) (*oidcDiscoveryDoc, error) {
	wellKnown := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	resp, err := uc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discovery request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery returned HTTP %d", resp.StatusCode)
	}
	var doc oidcDiscoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode discovery document: %w", err)
	}
	if doc.Issuer == "" || doc.AuthorizationEndpoint == "" ||
		doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, fmt.Errorf("discovery document missing required fields")
	}
	return &doc, nil
}

func (uc *OIDCTestUseCase) checkJWKS(ctx context.Context, jwksURI string) CheckJWKS {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return CheckJWKS{Error: fmt.Sprintf("build JWKS request: %s", err)}
	}
	resp, err := uc.client.Do(req)
	if err != nil {
		return CheckJWKS{Error: fmt.Sprintf("JWKS request failed: %s", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return CheckJWKS{Error: fmt.Sprintf("JWKS returned HTTP %d", resp.StatusCode)}
	}
	var body struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return CheckJWKS{Error: fmt.Sprintf("decode JWKS: %s", err)}
	}
	count := len(body.Keys)
	return CheckJWKS{OK: count > 0, Keys: count}
}

func (uc *OIDCTestUseCase) checkTokenEndpoint(ctx context.Context, tokenURL string) CheckTokenEndpoint {
	result := CheckTokenEndpoint{URL: tokenURL}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, tokenURL, nil)
	if err != nil {
		result.Error = fmt.Sprintf("build HEAD request: %s", err)
		return result
	}
	resp, err := uc.client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("HEAD request failed: %s", err)
		return result
	}
	_ = resp.Body.Close()
	// 2xx/4xx/405 = reachable; 5xx = server error = unreachable.
	result.OK = resp.StatusCode < 500
	if !result.OK {
		result.Error = fmt.Sprintf("token endpoint returned HTTP %d", resp.StatusCode)
	}
	return result
}
