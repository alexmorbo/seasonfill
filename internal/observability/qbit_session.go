package observability

import (
	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// qBit session telemetry. The age gauge is published by the
// torrentsync use case (the only long-lived qBit session in the
// process); the reauth counter is published by the qBit client at
// every re-login (NOT the first login). Reason label values are a
// closed set so Grafana queries stay stable.
const (
	MetricQbitSessionAgeSeconds = `seasonfill_qbit_session_age_seconds`
	MetricQbitReauthTotal       = `seasonfill_qbit_reauth_total`
)

// Reauth reason label values. Closed set — extending requires updating
// the qBit client classifier in tandem (see
// internal/shared/clients/qbit/client.go reauthReason* constants).
const (
	QbitReauthReasonCookieExpired = "cookie_expired"
	QbitReauthReasonNetworkError  = "network_error"
	QbitReauthReasonUnauthorized  = "unauthorized"
	QbitReauthReasonUnknown       = "unknown"
)

// SetQbitSessionAge publishes the seconds-since-last-successful-login
// for the named instance. Read out by the torrentsync use case on
// every tick from SyncSession.LoginAge. Operator alerts on age > 1h:
// the torrentsync sync session re-logs in transparently, so a session
// older than an hour suggests the cookie is *persistent* (good) OR
// the loop has stopped ticking (bad — cross-check with
// `seasonfill_torrentsync_last_refresh_at_seconds`).
func SetQbitSessionAge(instance domain.InstanceName, ageSec float64) {
	metrics.GetOrCreateGauge(
		`seasonfill_qbit_session_age_seconds{instance="`+string(instance)+`"}`, nil,
	).Set(ageSec)
}

// IncQbitReauth bumps the per-(instance, reason) counter. reason MUST
// be one of QbitReauthReason* constants. First-ever Login does NOT
// call this — the counter only increments on re-logins, so its rate
// directly measures session churn.
func IncQbitReauth(instance domain.InstanceName, reason string) {
	metrics.GetOrCreateCounter(
		`seasonfill_qbit_reauth_total{instance="` + string(instance) + `",reason="` + reason + `"}`,
	).Inc()
}

// QbitSessionMetricsAdapter satisfies the torrentsync UseCase's
// SetSessionAge port and the qBit client's IncReauth port. Zero value
// works.
type QbitSessionMetricsAdapter struct{}

func (QbitSessionMetricsAdapter) SetSessionAge(instance domain.InstanceName, ageSec float64) {
	SetQbitSessionAge(instance, ageSec)
}

func (QbitSessionMetricsAdapter) IncReauth(instance domain.InstanceName, reason string) {
	IncQbitReauth(instance, reason)
}
