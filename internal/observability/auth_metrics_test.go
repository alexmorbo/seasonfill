package observability

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seriesValue parses the numeric value of the exposition line whose series
// (name+labels) exactly matches series; returns 0 when absent, so a before/after
// delta reads cleanly as +N. Mirrors counterValue in middleware/metrics_test.go.
func seriesValue(t *testing.T, body, series string) float64 {
	t.Helper()
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, series+" ") {
			fields := strings.Fields(line)
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			require.NoError(t, err)
			return v
		}
	}
	return 0
}

func TestAuthLogin_AllModeResultCombos(t *testing.T) {
	combos := []struct{ mode, result string }{
		{AuthLoginModeForms, AuthLoginSuccess},
		{AuthLoginModeForms, AuthLoginFailure},
		{AuthLoginModeForms, AuthLoginRateLimited},
		{AuthLoginModeBasic, AuthLoginSuccess},
		{AuthLoginModeBasic, AuthLoginFailure},
		{AuthLoginModeBasic, AuthLoginRateLimited},
	}
	for _, cmb := range combos {
		series := `seasonfill_auth_login_total{mode="` + cmb.mode + `",result="` + cmb.result + `"}`
		before := seriesValue(t, writeAndRead(t), series)
		AuthLogin(cmb.mode, cmb.result)
		got := seriesValue(t, writeAndRead(t), series)
		assert.Equal(t, before+1, got, "series %s must increment by 1", series)
	}
}

func TestAuthSessionValidation_AllResults(t *testing.T) {
	results := []string{
		AuthSessionValid, AuthSessionExpired, AuthSessionBadSignature,
		AuthSessionStaleEpoch, AuthSessionMalformed, AuthSessionInvalid,
	}
	for _, res := range results {
		series := `seasonfill_auth_session_validations_total{result="` + res + `"}`
		before := seriesValue(t, writeAndRead(t), series)
		AuthSessionValidation(res)
		got := seriesValue(t, writeAndRead(t), series)
		assert.Equal(t, before+1, got, "series %s must increment by 1", series)
	}
}

func TestAuthSessionValidation_UnknownFallsToInvalid(t *testing.T) {
	series := `seasonfill_auth_session_validations_total{result="invalid"}`
	before := seriesValue(t, writeAndRead(t), series)
	AuthSessionValidation("some_future_sentinel")
	got := seriesValue(t, writeAndRead(t), series)
	assert.Equal(t, before+1, got, "unknown result must fall to the invalid catch-all")
}

func TestAuthOIDCCallback_AllResults(t *testing.T) {
	for _, res := range []string{
		AuthOIDCSuccess, AuthOIDCFailure, AuthOIDCGroupDenied, AuthOIDCError,
	} {
		series := `seasonfill_auth_oidc_callback_total{result="` + res + `"}`
		before := seriesValue(t, writeAndRead(t), series)
		AuthOIDCCallback(res)
		got := seriesValue(t, writeAndRead(t), series)
		assert.Equal(t, before+1, got, "series %s must increment by 1", series)
	}
}

func TestAuthMetricNames_Frozen(t *testing.T) {
	assert.Equal(t, "seasonfill_auth_login_total", MetricAuthLoginTotal)
	assert.Equal(t, "seasonfill_auth_session_validations_total", MetricAuthSessionValidationsTotal)
	assert.Equal(t, "seasonfill_auth_oidc_callback_total", MetricAuthOIDCCallbackTotal)
}
