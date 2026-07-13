package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// TestBasicAuth_HappyPath_DoesNotTickLoginSuccess pins F-09: basic auth is
// per-request, so a successful authenticated request must NOT increment
// seasonfill_auth_login_total{mode="basic",result="success"} (which would count
// requests, not logins). Non-parallel: reads a global counter delta.
func TestBasicAuth_HappyPath_DoesNotTickLoginSuccess(t *testing.T) {
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	const success = `seasonfill_auth_login_total{mode="basic",result="success"}`
	before := counterValue(t, dump(), success)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("Authorization", basicHeader("admin", "hunter22"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	assert.Equal(t, before, counterValue(t, dump(), success),
		"successful per-request basic auth must NOT tick the login-success counter")
}

// TestBasicAuth_BadCreds_StillTicksLoginFailure asserts the retained signal:
// a failed basic-auth attempt is a genuine failed login and must still bump
// seasonfill_auth_login_total{mode="basic",result="failure"}. Non-parallel.
func TestBasicAuth_BadCreds_StillTicksLoginFailure(t *testing.T) {
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	const failure = `seasonfill_auth_login_total{mode="basic",result="failure"}`
	before := counterValue(t, dump(), failure)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("Authorization", basicHeader("admin", "wrong"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	assert.Equal(t, before+1, counterValue(t, dump(), failure),
		"a failed basic-auth attempt is a genuine failed login and must still be counted")
}
