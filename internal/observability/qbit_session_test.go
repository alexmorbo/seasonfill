package observability

import (
	"strings"
	"testing"
)

func TestSetQbitSessionAge_EmitsGauge(t *testing.T) {
	t.Parallel()
	SetQbitSessionAge("session_alpha", 1800)
	body := dumpRegistry()
	const want = `seasonfill_qbit_session_age_seconds{instance="session_alpha"}`
	if !strings.Contains(body, want) {
		t.Fatalf("missing gauge: %s\n%s", want, body)
	}
}

func TestIncQbitReauth_EveryReason(t *testing.T) {
	t.Parallel()
	for _, r := range []string{
		QbitReauthReasonCookieExpired, QbitReauthReasonNetworkError,
		QbitReauthReasonUnauthorized, QbitReauthReasonUnknown,
	} {
		IncQbitReauth("reauth_alpha", r)
	}
	body := dumpRegistry()
	for _, r := range []string{"cookie_expired", "network_error", "unauthorized", "unknown"} {
		want := `seasonfill_qbit_reauth_total{instance="reauth_alpha",reason="` + r + `"}`
		if !strings.Contains(body, want) {
			t.Errorf("missing counter: %s\n%s", want, body)
		}
	}
}

func TestQbitSessionMetricsAdapter_Dispatches(t *testing.T) {
	t.Parallel()
	a := QbitSessionMetricsAdapter{}
	a.SetSessionAge("session_delta", 999)
	a.IncReauth("session_delta", QbitReauthReasonCookieExpired)
	body := dumpRegistry()
	for _, w := range []string{
		`seasonfill_qbit_session_age_seconds{instance="session_delta"}`,
		`seasonfill_qbit_reauth_total{instance="session_delta",reason="cookie_expired"}`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("adapter missed %q:\n%s", w, body)
		}
	}
}
