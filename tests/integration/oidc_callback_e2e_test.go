//go:build integration_e2e

package integration

import (
	"testing"
)

// This integration test exercises the full OIDC Authorization Code +
// PKCE flow against an httptest-mounted minimal OIDC provider.
//
// Stub server responsibilities (Implementation Agent to flesh out):
//
//  1. GET /.well-known/openid-configuration — serve issuer URL +
//     authorization_endpoint + token_endpoint + jwks_uri pointing at
//     the same httptest.Server.
//  2. GET /jwks — serve the RSA public key as a JWKS document.
//  3. GET /authorize — accept client_id, redirect_uri, state,
//     code_challenge, code_challenge_method=S256, scope, nonce.
//     Persist (code, nonce, code_challenge, sub) in a map and 302
//     to redirect_uri?code=X&state=...
//  4. POST /token — accept grant_type=authorization_code, code,
//     code_verifier, redirect_uri, client_id, client_secret. Verify
//     sha256(code_verifier) base64-rawurl-encoded matches the stored
//     code_challenge. Sign an ID token JWT (RS256) with claims:
//     {iss, aud=client_id, sub, nonce, exp, preferred_username,
//     groups}. Return {access_token, id_token, token_type=Bearer,
//     expires_in}.
//
// The full E2E test then:
//   - Provisions a runtime_config row with auth_mode=oidc + the stub
//     server's issuer URL, client_id, redirect_url pointing at the
//     seasonfill HTTP server.
//   - Hits GET /api/v1/auth/oidc/start, follows the redirect, captures
//     the cookies.
//   - Stub /authorize redirects to /api/v1/auth/oidc/callback?code=X&state=Y.
//   - Asserts the response sets a `seasonfill_session` cookie + 302s
//     to /.
//   - Asserts the admin_users row exists with oidc_subject populated.
//   - Verifies a follow-up GET /api/v1/instances with the session
//     cookie returns 200.
//
// The stub server lives in `internal/testsupport/oidcstub/oidcstub.go`
// (NEW; Implementation Agent creates) so it can be shared across this
// test and any future OIDC E2E. Use `github.com/golang-jwt/jwt/v5` for
// signing.

func TestOIDC_FullCallbackFlow_MintsCookie(t *testing.T) {
	t.Parallel()
	t.Skip("Implementation Agent: build stub server + flesh out per file comment")
}

func TestOIDC_GroupACL_Denied(t *testing.T) {
	t.Parallel()
	t.Skip("Implementation Agent: build stub server + flesh out per file comment")
}

func TestOIDC_GroupACL_AllowedWhenIntersects(t *testing.T) {
	t.Parallel()
	t.Skip("Implementation Agent: build stub server + flesh out per file comment")
}
