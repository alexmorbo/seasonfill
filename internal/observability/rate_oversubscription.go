package observability

import (
	"context"
	"log/slog"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// MetricSonarrRateOversubscribed is 1 when the global Sonarr rate limiter has a
// FINITE RPM budget (>0) that is smaller than the sum of the per-instance RPM
// floors, else 0. Over-subscription is a legal operator choice (SI-7) — this
// gauge and its paired WARN only make the starvation risk (R7) visible; the
// config write is never rejected.
const MetricSonarrRateOversubscribed = `seasonfill_sonarr_rate_oversubscribed`

// CheckRateOversubscription evaluates the provisioned-floor invariant and
// publishes MetricSonarrRateOversubscribed (0/1). Called from every snapshot
// publish (runtimeconfig edit, instance CRUD, boot).
//
// Gate: when globalRPM <= 0 the global limiter is unlimited/disabled (see
// ratelimit.NewFromRPM's nil-sentinel) — there is no finite ceiling to
// over-subscribe against, so the gauge is forced to 0 and no WARN fires.
// Otherwise it sums RateLimit.RPM across ALL instances (seasonfill has no
// per-instance enable/disable flag — every configured instance consumes the
// shared limiter). If the finite global budget is below that sum it emits a
// single WARN and sets the gauge to 1. Never returns an error and never rejects.
func CheckRateOversubscription(ctx context.Context, log *slog.Logger, globalRPM int, instances []runtime.InstanceSnapshot) {
	if globalRPM <= 0 {
		setRateOversubscribed(false)
		return
	}
	sum := 0
	for _, inst := range instances {
		sum += inst.RateLimit.RPM
	}
	over := globalRPM < sum
	if over && log != nil {
		log.WarnContext(ctx, "sonarr.rate_limit.oversubscribed",
			slog.Int("global_rpm", globalRPM),
			slog.Int("sum_instance_rpm", sum),
			slog.Int("instance_count", len(instances)))
	}
	setRateOversubscribed(over)
}

func setRateOversubscribed(over bool) {
	v := 0.0
	if over {
		v = 1.0
	}
	metrics.GetOrCreateGauge(MetricSonarrRateOversubscribed, nil).Set(v)
}
