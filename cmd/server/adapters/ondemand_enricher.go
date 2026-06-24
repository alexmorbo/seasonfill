package adapters

import (
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/observability"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ondemandThrottleTTL — how long after an enqueue a duplicate request for
// the same seriesID is suppressed. Chosen to match the user's expected
// detail-page reload cadence (5-30s window during which the SPA re-polls).
const ondemandThrottleTTL = 30 * time.Second

// ondemandSweepInterval — how often expired throttle entries are evicted.
// Cheap loop; 5min keeps memory bounded even on never-restarting prod pods.
const ondemandSweepInterval = 5 * time.Minute

// OnDemandEnricherHolder satisfies seriesdetail.OnDemandEnricher and forwards
// to a late-bound enrichment.Dispatcher. Same pattern as PersonEnqueuerHolder
// (cmd/server/adapters/person_enqueuer.go) — the dispatcher doesn't exist at
// BuildSeriesDetail time, so server.go's LATE BIND ZONE calls Set() after
// wireEnrichment returns.
//
// Throttle: a 30s TTL map keyed on series.id suppresses duplicate enqueues
// (the SPA re-polls /series/{id} every 5-10s while enrichment runs; without
// throttling the user would generate one PriorityHot job per poll).
type OnDemandEnricherHolder struct {
	log *slog.Logger

	mu       sync.Mutex
	inner    appenrich.Dispatcher
	throttle map[domain.SeriesID]time.Time
	closed   bool
	stopCh   chan struct{}
}

// Compile-time assertion: the holder satisfies the seriesdetail port.
var _ seriesdetail.OnDemandEnricher = (*OnDemandEnricherHolder)(nil)

// NewOnDemandEnricherHolder returns a holder with no inner dispatcher and an
// empty throttle map. Starts a background sweep goroutine. Call Close at
// shutdown to stop the sweep.
func NewOnDemandEnricherHolder(base *slog.Logger) *OnDemandEnricherHolder {
	if base == nil {
		base = slog.Default()
	}
	h := &OnDemandEnricherHolder{
		log:      sharedports.DomainLogger(base, "enrichment"),
		throttle: make(map[domain.SeriesID]time.Time),
		stopCh:   make(chan struct{}),
	}
	go h.sweepLoop()
	return h
}

// Set wires the inner dispatcher. Idempotent — the production boot path
// calls Set at most once, after wireEnrichment returns.
func (h *OnDemandEnricherHolder) Set(d appenrich.Dispatcher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inner = d
}

// EnqueueIfStale is the public seriesdetail.OnDemandEnricher impl.
//
// Returns immediately. The actual dispatcher call runs in a goroutine so
// the detail-page request returns its 200 without waiting on the (cheap
// but non-zero) queue mutex acquisition.
func (h *OnDemandEnricherHolder) EnqueueIfStale(seriesID domain.SeriesID, hydration series.Hydration) {
	if hydration == series.HydrationFull {
		observability.IncOnDemandEnrich("skipped_full")
		return
	}
	if seriesID <= 0 {
		observability.IncOnDemandEnrich("skipped_invalid_id")
		return
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		observability.IncOnDemandEnrich("skipped_closed")
		return
	}
	now := time.Now()
	if last, ok := h.throttle[seriesID]; ok && now.Sub(last) < ondemandThrottleTTL {
		h.mu.Unlock()
		observability.IncOnDemandEnrich("throttled")
		h.log.Debug("enrichment.ondemand.throttled",
			slog.Int64("series_id", int64(seriesID)),
			slog.Int64("since_last_ms", now.Sub(last).Milliseconds()),
		)
		return
	}
	h.throttle[seriesID] = now
	inner := h.inner
	h.mu.Unlock()

	if inner == nil {
		observability.IncOnDemandEnrich("skipped_no_dispatcher")
		h.log.Debug("enrichment.ondemand.no_dispatcher",
			slog.Int64("series_id", int64(seriesID)),
		)
		return
	}

	// Fire-and-forget. Detached from request ctx — the user's request
	// returns its 200 immediately; we keep the enqueue mutex acquisition
	// off the response path. Enqueue itself is a non-blocking channel-send
	// with timeout per priorityQueue.
	go func() {
		// Recover so a misbehaving dispatcher impl can never crash the
		// pod via the goroutine.
		defer func() {
			if r := recover(); r != nil {
				observability.IncOnDemandEnrich("panic")
				h.log.Error("enrichment.ondemand.panic",
					slog.Int64("series_id", int64(seriesID)),
					slog.Any("recovered", r),
				)
			}
		}()
		inner.Enqueue(appenrich.EntitySeries, int64(seriesID), appenrich.PriorityHot)
		observability.IncOnDemandEnrich("enqueued")
		h.log.Info("enrichment.ondemand.enqueued",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("hydration", string(hydration)),
			slog.String("priority", "hot"),
		)
	}()
}

// Close stops the sweep goroutine. Idempotent.
func (h *OnDemandEnricherHolder) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	close(h.stopCh)
	h.mu.Unlock()
}

// sweepLoop evicts throttle entries older than ondemandThrottleTTL on a
// periodic ticker. Keeps the map from growing unbounded over long uptimes.
func (h *OnDemandEnricherHolder) sweepLoop() {
	ticker := time.NewTicker(ondemandSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.sweep()
		}
	}
}

func (h *OnDemandEnricherHolder) sweep() {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for k, t := range h.throttle {
		if now.Sub(t) >= ondemandThrottleTTL {
			delete(h.throttle, k)
		}
	}
}
