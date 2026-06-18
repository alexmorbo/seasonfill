package observability

import (
	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MetricTorrentsyncUnmapped is the per-instance unmapped-torrents
// gauge emitted by the reconciler after every pass. PRD §4.5 +
// story 221 acceptance criteria.
const MetricTorrentsyncUnmapped = `seasonfill_torrentsync_unmapped`

// SetTorrentsyncUnmapped replaces the gauge value for the named
// instance. Called from application/torrentsync.Reconciler.run.
func SetTorrentsyncUnmapped(instance domain.InstanceName, count int) {
	metrics.GetOrCreateGauge(`seasonfill_torrentsync_unmapped{instance="`+string(instance)+`"}`, nil).Set(float64(count))
}

// TorrentsyncMetricsAdapter satisfies
// application/torrentsync.UnmappedGauge. Zero value is fully
// functional — pass it by value at construction.
type TorrentsyncMetricsAdapter struct{}

// SetTorrentsyncUnmapped implements torrentsync.UnmappedGauge.
func (TorrentsyncMetricsAdapter) SetTorrentsyncUnmapped(instance domain.InstanceName, count int) {
	SetTorrentsyncUnmapped(instance, count)
}
