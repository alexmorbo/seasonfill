package rest

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	infraoidc "github.com/alexmorbo/seasonfill/internal/admin/infrastructure/oidc"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// metricValue renders the global VM set and parses the numeric value of the
// exposition line whose series matches; 0 when absent (delta reads as +1).
func metricValue(t *testing.T, series string) float64 {
	t.Helper()
	buf := &strings.Builder{}
	observability.WritePrometheus(buf)
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(line, series+" ") {
			fields := strings.Fields(line)
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			require.NoError(t, err)
			return v
		}
	}
	return 0
}

func TestLoginMetric_SuccessAndFailure(t *testing.T) {
	r, _ := setupAuth(t, seedRepo(t), nil)

	okSeries := `seasonfill_auth_login_total{mode="forms",result="success"}`
	failSeries := `seasonfill_auth_login_total{mode="forms",result="failure"}`
	okBefore := metricValue(t, okSeries)
	failBefore := metricValue(t, failSeries)

	wOK := postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "hunter22"}, nil)
	require.Equal(t, http.StatusOK, wOK.Code, "body=%s", wOK.Body.String())

	wBad := postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "wrong"}, nil)
	require.Equal(t, http.StatusUnauthorized, wBad.Code)

	assert.Equal(t, okBefore+1, metricValue(t, okSeries), "success login must Inc")
	assert.Equal(t, failBefore+1, metricValue(t, failSeries), "failed login must Inc")
}

func TestLoginMetric_RateLimited(t *testing.T) {
	lim := auth.NewIPLimiter(rate.Every(time.Hour), 1)
	r, _ := setupAuth(t, seedRepo(t), lim)
	series := `seasonfill_auth_login_total{mode="forms",result="rate_limited"}`
	before := metricValue(t, series)

	// First attempt consumes the burst (wrong password → 401).
	_ = postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "wrong"}, nil)
	// Second attempt is throttled → 429 → rate_limited Inc.
	w := postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "hunter22"}, nil)
	require.Equal(t, http.StatusTooManyRequests, w.Code)

	assert.Equal(t, before+1, metricValue(t, series), "429 login must Inc rate_limited")
}

func TestSessionValidationMetric_ValidAndBadSignature(t *testing.T) {
	r, h := setupAuth(t, seedRepo(t), nil)
	validSeries := `seasonfill_auth_session_validations_total{result="valid"}`
	badSeries := `seasonfill_auth_session_validations_total{result="bad_signature"}`
	validBefore := metricValue(t, validSeries)
	badBefore := metricValue(t, badSeries)

	// Valid cookie signed with the handler's key → RequireAuth VerifySession ok.
	tok, err := middleware.SignSession(h.sessionKey, "admin", time.Now().Add(time.Hour), 0)
	require.NoError(t, err)
	reqOK := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/auth/session", nil)
	reqOK.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: tok})
	wOK := httptest.NewRecorder()
	r.ServeHTTP(wOK, reqOK)
	require.Equal(t, http.StatusOK, wOK.Code)

	// Tampered cookie (valid base64 shape, wrong HMAC) → bad_signature. Take the
	// good token's body segment and replace the signature segment.
	parts := strings.SplitN(tok, ".", 2)
	require.Len(t, parts, 2)
	tampered := parts[0] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	reqBad := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/auth/session", nil)
	reqBad.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: tampered})
	wBad := httptest.NewRecorder()
	r.ServeHTTP(wBad, reqBad)
	require.Equal(t, http.StatusUnauthorized, wBad.Code)

	assert.Equal(t, validBefore+1, metricValue(t, validSeries), "valid cookie must Inc valid")
	assert.Equal(t, badBefore+1, metricValue(t, badSeries), "tampered cookie must Inc bad_signature")
}

func TestOIDCCallbackMetric_FailureOnStateMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{Mode: runtime.AuthModeOIDC})
	uc := auth.NewOIDCLoginUseCase(infraoidc.NewProviderCache(), stubAdminRepo{})
	h := NewOIDCHandler(uc, ptr, []byte("k"), time.Hour, false, nil)

	r := gin.New()
	r.GET("/api/v1/auth/oidc/callback", h.Callback)

	series := `seasonfill_auth_oidc_callback_total{result="failure"}`
	before := metricValue(t, series)

	// No state cookie set (ExpectedState="") but a non-empty state query →
	// usecase returns ErrOIDCStateMismatch before any network call.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/auth/oidc/callback?code=abc&state=mismatch", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())

	assert.Equal(t, before+1, metricValue(t, series), "state mismatch must Inc oidc failure")
}
