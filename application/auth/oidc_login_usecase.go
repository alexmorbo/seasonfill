package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/admin"
	infraoidc "github.com/alexmorbo/seasonfill/infrastructure/oidc"
)

var (
	ErrOIDCNotConfigured   = errors.New("oidc: not configured")
	ErrOIDCStateMismatch   = errors.New("oidc: state mismatch")
	ErrOIDCNonceMismatch   = errors.New("oidc: nonce mismatch")
	ErrOIDCGroupDenied     = errors.New("oidc: group ACL denied")
	ErrOIDCMissingUsername = errors.New("oidc: missing username claim")
)

// RequestInfo carries HTTP request headers used to derive the OIDC redirect URL
// when the stored configuration leaves redirect_url blank.
type RequestInfo struct {
	Host string // r.Host
	XFH  string // X-Forwarded-Host
	XFP  string // X-Forwarded-Proto
}

// DerivedRedirectURL returns the OIDC callback URL Start/Callback must
// pass to the provider. Explicit cfg.RedirectURL wins. Otherwise:
// scheme = XFP || "https"; host = XFH || Host; suffix = "/api/v1/auth/oidc/callback".
func DerivedRedirectURL(cfg OIDCConfig, info RequestInfo) string {
	if cfg.RedirectURL != "" {
		return cfg.RedirectURL
	}
	scheme := "https"
	if s := strings.TrimSpace(info.XFP); s != "" {
		scheme = s
	}
	host := info.XFH
	if host == "" {
		host = info.Host
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host + "/api/v1/auth/oidc/callback"
}

type OIDCConfig struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	UsernameClaim string
	AllowedGroups []string
	GroupsClaim   string
}

// StartResult carries everything the HTTP handler needs to set short-
// lived cookies and 302-redirect to the provider authorization endpoint.
type StartResult struct {
	AuthURL      string
	State        string
	Nonce        string
	PKCEVerifier string
}

// CallbackInput carries the cookie-bound values the handler extracts
// from the inbound callback request.
type CallbackInput struct {
	Code          string
	State         string
	ExpectedState string
	Nonce         string
	PKCEVerifier  string
}

// CallbackResult carries the verified identity for the handler to mint
// a session cookie around.
type CallbackResult struct {
	Username string
	Subject  string
}

// OIDCLoginUseCase orchestrates the Authorization Code + PKCE flow.
// Stateless: every method receives the per-request OIDCConfig snapshot
// and returns plain data; no shared state beyond the provider cache.
type OIDCLoginUseCase struct {
	providers *infraoidc.ProviderCache
	admins    ports.AdminUserRepository
}

func NewOIDCLoginUseCase(cache *infraoidc.ProviderCache, admins ports.AdminUserRepository) *OIDCLoginUseCase {
	return &OIDCLoginUseCase{providers: cache, admins: admins}
}

// Start generates PKCE verifier (RFC 7636 S256), random state, nonce,
// and builds the authorization URL. Caller stores state/nonce/verifier
// in HTTPOnly cookies and 302-redirects to AuthURL.
// info is used to derive the redirect_url when cfg.RedirectURL is empty.
func (u *OIDCLoginUseCase) Start(ctx context.Context, cfg OIDCConfig, info RequestInfo) (StartResult, error) {
	if cfg.Issuer == "" {
		return StartResult{}, ErrOIDCNotConfigured
	}
	resolvedRedirect := DerivedRedirectURL(cfg, info)
	if resolvedRedirect == "" {
		return StartResult{}, errors.New("oidc: cannot derive redirect_url from request headers")
	}
	provider, err := u.providers.Get(ctx, cfg.Issuer)
	if err != nil {
		return StartResult{}, err
	}
	state, err := randomToken(32)
	if err != nil {
		return StartResult{}, fmt.Errorf("oidc: gen state: %w", err)
	}
	nonce, err := randomToken(32)
	if err != nil {
		return StartResult{}, fmt.Errorf("oidc: gen nonce: %w", err)
	}
	verifier, err := randomToken(64)
	if err != nil {
		return StartResult{}, fmt.Errorf("oidc: gen pkce verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	conf := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  resolvedRedirect,
		Scopes:       scopes,
	}
	authURL := conf.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		gooidc.Nonce(nonce),
	)
	return StartResult{
		AuthURL: authURL, State: state, Nonce: nonce, PKCEVerifier: verifier,
	}, nil
}

// Callback exchanges code → tokens, verifies the ID token (signature,
// issuer, audience, nonce, expiry), enforces the group ACL, and maps
// the subject to an admin row. Returns CallbackResult or an error
// suitable for surfacing to the operator (specifics are intentionally
// vague at the HTTP layer — the handler logs the err with detail and
// returns a generic 401).
// info is used to derive the redirect_url when cfg.RedirectURL is empty;
// it must match the value used by Start (redirect_uri must match).
func (u *OIDCLoginUseCase) Callback(ctx context.Context, cfg OIDCConfig, info RequestInfo, in CallbackInput) (CallbackResult, error) {
	if cfg.Issuer == "" {
		return CallbackResult{}, ErrOIDCNotConfigured
	}
	if in.State == "" || in.State != in.ExpectedState {
		return CallbackResult{}, ErrOIDCStateMismatch
	}
	resolvedRedirect := DerivedRedirectURL(cfg, info)
	if resolvedRedirect == "" {
		return CallbackResult{}, errors.New("oidc: cannot derive redirect_url from request headers")
	}
	provider, err := u.providers.Get(ctx, cfg.Issuer)
	if err != nil {
		return CallbackResult{}, err
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	conf := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  resolvedRedirect,
		Scopes:       scopes,
	}
	tok, err := conf.Exchange(ctx, in.Code,
		oauth2.SetAuthURLParam("code_verifier", in.PKCEVerifier),
	)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("oidc: token exchange: %w", err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return CallbackResult{}, errors.New("oidc: id_token missing from token response")
	}
	verifier := infraoidc.Verifier(provider, cfg.ClientID)
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("oidc: id_token verify: %w", err)
	}
	if idToken.Nonce != in.Nonce {
		return CallbackResult{}, ErrOIDCNonceMismatch
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return CallbackResult{}, fmt.Errorf("oidc: claims decode: %w", err)
	}

	if !groupACLAllows(claims, cfg.GroupsClaim, cfg.AllowedGroups) {
		return CallbackResult{}, ErrOIDCGroupDenied
	}

	usernameClaim := cfg.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "preferred_username"
	}
	username := stringClaim(claims, usernameClaim)
	if username == "" {
		// Fallback chain: try `email`, then the subject. We never want
		// to mint a cookie with an empty username (HMAC verify rejects
		// p.Username=="" as malformed).
		if u := stringClaim(claims, "email"); u != "" {
			username = u
		} else if idToken.Subject != "" {
			username = idToken.Subject
		}
	}
	if username == "" {
		return CallbackResult{}, ErrOIDCMissingUsername
	}

	row, err := u.admins.GetByOIDCSubject(ctx, idToken.Subject)
	switch {
	case err == nil:
		// hit — reuse
	case errors.Is(err, ports.ErrNotFound):
		row, err = u.admins.CreateFromOIDC(ctx, idToken.Subject, username)
		if err != nil {
			return CallbackResult{}, fmt.Errorf("oidc: create admin row: %w", err)
		}
	default:
		return CallbackResult{}, fmt.Errorf("oidc: lookup admin: %w", err)
	}
	_ = admin.AdminUser{} // keep admin import alive in builds that don't read the row further

	return CallbackResult{Username: row.Username, Subject: idToken.Subject}, nil
}

// groupACLAllows: empty allowed → allow all. Non-empty → require at least one
// element of the configured claim path to match. claimPath is dot-separated
// (e.g. "groups" or "realm_access.roles"). Scalar string claim values are
// treated as a single-element list to handle providers that emit a plain
// string when the user belongs to exactly one group.
func groupACLAllows(claims map[string]any, claimPath string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	got := extractStringSlicePermissive(claims, claimPath)
	if len(got) == 0 {
		return false
	}
	allowSet := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allowSet[strings.TrimSpace(a)] = struct{}{}
	}
	for _, g := range got {
		if _, ok := allowSet[strings.TrimSpace(g)]; ok {
			return true
		}
	}
	return false
}

// extractStringSlice walks a dot-separated path through a JSON-decoded claims
// map and returns its terminal value as a string slice. Missing keys,
// non-map intermediates, scalar terminals, and empty paths all return nil.
func extractStringSlice(claims map[string]any, path string) []string {
	if path == "" {
		return nil
	}
	segs := strings.Split(path, ".")
	var cur any = claims
	for _, seg := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		next, exists := m[seg]
		if !exists {
			return nil
		}
		cur = next
	}
	switch t := cur.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// extractStringSlicePermissive is like extractStringSlice but also handles
// scalar string terminals by treating them as single-element slices. Used
// by groupACLAllows to support providers that emit a plain string when the
// user belongs to exactly one group.
func extractStringSlicePermissive(claims map[string]any, path string) []string {
	if path == "" {
		return nil
	}
	segs := strings.Split(path, ".")
	var cur any = claims
	for _, seg := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		next, exists := m[seg]
		if !exists {
			return nil
		}
		cur = next
	}
	return stringSliceFromClaim(cur)
}

func stringClaim(claims map[string]any, key string) string {
	v, ok := claims[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func stringSliceFromClaim(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	}
	return nil
}

func randomToken(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SafeReturnTo returns a same-origin path or "/" — applied to the
// `next` query param to defend against open-redirect.
func SafeReturnTo(raw string) string {
	if raw == "" {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	if u.IsAbs() || u.Host != "" {
		return "/"
	}
	if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return u.RequestURI()
}
