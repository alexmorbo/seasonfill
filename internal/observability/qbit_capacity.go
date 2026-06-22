package observability

import (
	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MetricQbitTorrentsRows is the per-instance gauge for the
// qbit_torrents row count. Filled by the periodic capacity collector
// (cmd/server/loops/qbit_capacity.go) every 60s.
const MetricQbitTorrentsRows = `seasonfill_qbit_torrents_rows`

// SetQbitTorrentsRows replaces the gauge value for the named instance.
// count is the number of `present=true` rows in qbit_torrents.
func SetQbitTorrentsRows(instance domain.InstanceName, count int) {
	metrics.GetOrCreateGauge(
		`seasonfill_qbit_torrents_rows{instance="`+string(instance)+`"}`, nil,
	).Set(float64(count))
}

// QbitCapacityMetricsAdapter satisfies the cmd/server/loops capacity
// collector's narrow metrics port. Zero value works.
type QbitCapacityMetricsAdapter struct{}

// SetQbitTorrentsRows implements loops.QbitCapacityMetrics.
func (QbitCapacityMetricsAdapter) SetQbitTorrentsRows(instance domain.InstanceName, count int) {
	SetQbitTorrentsRows(instance, count)
}
