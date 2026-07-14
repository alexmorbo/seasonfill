package observability

import "github.com/VictoriaMetrics/metrics"

// Story M-7 — auth-surface observability. Three counters covering the login /
// session-validation / oidc-callback terminal branches. Every label value is a
// compile-time literal (see the metric-name constants below): NEVER a username,
// email, subject id, token, ip, or api-key — those are unbounded / PII and are
// forbidden as labels. Metric NAMES are frozen; label KEYS are frozen; new label
// VALUES within the documented enums are safe.

// Metric names (frozen).
const (
	MetricAuthLoginTotal              = "seasonfill_auth_login_total"
	MetricAuthSessionValidationsTotal = "seasonfill_auth_session_validations_total"
	MetricAuthOIDCCallbackTotal       = "seasonfill_auth_oidc_callback_total"
)

// auth_login_total label values.
const (
	AuthLoginModeForms   = "forms"
	AuthLoginSuccess     = "success"
	AuthLoginFailure     = "failure"
	AuthLoginRateLimited = "rate_limited"
)

// auth_session_validations_total result values (bounded).
const (
	AuthSessionValid        = "valid"
	AuthSessionExpired      = "expired"
	AuthSessionBadSignature = "bad_signature"
	AuthSessionStaleEpoch   = "stale_epoch"
	AuthSessionMalformed    = "malformed"
	AuthSessionInvalid      = "invalid" // catch-all for future sentinels
)

// auth_oidc_callback_total result values.
const (
	AuthOIDCSuccess     = "success"
	AuthOIDCFailure     = "failure"
	AuthOIDCGroupDenied = "group_denied"
	AuthOIDCError       = "error"
)

// Session-validation counters are pre-resolved once: this counter is Inc'd on
// EVERY authenticated request, so we avoid a per-call string build + registry
// lookup by caching the *Counter pointers at init and selecting by switch.
var (
	authSessValid        = sessionCounter(AuthSessionValid)
	authSessExpired      = sessionCounter(AuthSessionExpired)
	authSessBadSignature = sessionCounter(AuthSessionBadSignature)
	authSessStaleEpoch   = sessionCounter(AuthSessionStaleEpoch)
	authSessMalformed    = sessionCounter(AuthSessionMalformed)
	authSessInvalid      = sessionCounter(AuthSessionInvalid)
)

func sessionCounter(result string) *metrics.Counter {
	return metrics.GetOrCreateCounter(
		`seasonfill_auth_session_validations_total{result="` + result + `"}`)
}

// AuthLogin bumps the login-outcome counter. mode is always "forms" (OIDC
// logins are counted via AuthOIDCCallback); result ∈ {success,failure,
// rate_limited}. Both are compile-time literals from the call site — never
// user input. Cold path (one Inc per login attempt), so the concatenation
// idiom is fine.
func AuthLogin(mode, result string) {
	metrics.GetOrCreateCounter(
		`seasonfill_auth_login_total{mode="` + mode + `",result="` + result + `"}`).Inc()
}

// AuthSessionValidation bumps the per-result session-validation counter. HOT
// PATH: fires on every authenticated request. Allocation-free — selects a
// pre-resolved *Counter by the bounded result label; unknown values fall to the
// {result="invalid"} catch-all.
func AuthSessionValidation(result string) {
	switch result {
	case AuthSessionValid:
		authSessValid.Inc()
	case AuthSessionExpired:
		authSessExpired.Inc()
	case AuthSessionBadSignature:
		authSessBadSignature.Inc()
	case AuthSessionStaleEpoch:
		authSessStaleEpoch.Inc()
	case AuthSessionMalformed:
		authSessMalformed.Inc()
	default:
		authSessInvalid.Inc()
	}
}

// AuthOIDCCallback bumps the OIDC-callback-outcome counter. result ∈
// {success,failure,group_denied,error} — a compile-time literal. Cold path.
func AuthOIDCCallback(result string) {
	metrics.GetOrCreateCounter(
		`seasonfill_auth_oidc_callback_total{result="` + result + `"}`).Inc()
}
