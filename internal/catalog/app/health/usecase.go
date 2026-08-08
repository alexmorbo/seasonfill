// Package health assembles the read-only catalog-health "pulse" report
// backing GET /api/v1/insights/health. It owns the staleness TTL policy
// (reused from the enrichment refresh scheduler) and the deferred-signal
// metadata for rate-limit pressure; the five DB queries live behind a
// narrow HealthRepository port.
package health

import (
	"context"
	"fmt"
	"time"

	enrichment "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/observability"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

const (
	// drillDownLimit bounds every signal's item list. 50 is a comfortable
	// operator triage window without turning the pulse into a full export.
	drillDownLimit = 50
	// stuckGrabAge is how long a grab may sit in non-terminal 'grabbed'
	// before it is a "stuck" orphan. 24h is well past a healthy
	// grab→import round-trip.
	stuckGrabAge = 24 * time.Hour
	// stuckGrabNote disambiguates this DB signal from the runtime metric.
	stuckGrabNote = "grabs stuck in non-terminal 'grabbed' older than 24h; " +
		"distinct from seasonfill_webhook_orphan_total (webhook events with no matching grab row)"
	// rateLimitReason describes the deferred rate-limit signal.
	rateLimitReason = "config-derived 0/1 gauge, not a DB rowset — see the metric on /metrics + Grafana"
)

// SeriesSignal is a count + series drill-down (tvdb / poster signals).
type SeriesSignal struct {
	Count int
	Items []ports.HealthSeriesItem
}

// StaleSignal is a count + stale drill-down.
type StaleSignal struct {
	Count int
	Items []ports.HealthStaleItem
}

// GrabSignal is a count + grab drill-down plus a disambiguating note.
type GrabSignal struct {
	Count int
	Note  string
	Items []ports.HealthGrabItem
}

// InboxSignal is a count + dead-letter drill-down.
type InboxSignal struct {
	Count int
	Items []ports.HealthInboxItem
}

// DeferredSignal marks a signal that is intentionally not computed here,
// pointing the operator at where it currently lives.
type DeferredSignal struct {
	Deferred bool
	Reason   string
	Metric   string
}

// Report is the assembled catalog-health pulse.
type Report struct {
	GeneratedAt       time.Time
	MissingTVDBID     SeriesSignal
	MissingPoster     SeriesSignal
	StaleEnrichment   StaleSignal
	StuckGrabs        GrabSignal
	DeadLetters       InboxSignal
	RateLimitPressure DeferredSignal
}

// UseCase builds the Report from the HealthRepository port.
type UseCase struct {
	repo  ports.HealthRepository
	clock func() time.Time
	ttl   enrichment.RefreshTTL
}

// NewUseCase wires the read-only health usecase. Clock defaults to
// time.Now().UTC; TTL to DefaultRefreshTTL (7d/14d/30d).
func NewUseCase(repo ports.HealthRepository) *UseCase {
	return &UseCase{
		repo:  repo,
		clock: func() time.Time { return time.Now().UTC() },
		ttl:   enrichment.DefaultRefreshTTL(),
	}
}

// WithClock swaps the clock for deterministic tests.
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase {
	uc.clock = clock
	return uc
}

// Build runs the five signal queries sequentially and returns the
// assembled report. Any query error aborts (the pulse is all-or-nothing;
// a partial dashboard would mislead the operator).
func (uc *UseCase) Build(ctx context.Context) (Report, error) {
	now := uc.clock()
	rep := Report{GeneratedAt: now}

	tvdbCount, tvdbItems, err := uc.repo.MissingTVDBID(ctx, drillDownLimit)
	if err != nil {
		return Report{}, fmt.Errorf("health build: missing_tvdb_id: %w", err)
	}
	rep.MissingTVDBID = SeriesSignal{Count: tvdbCount, Items: tvdbItems}

	posterCount, posterItems, err := uc.repo.MissingPoster(ctx, drillDownLimit)
	if err != nil {
		return Report{}, fmt.Errorf("health build: missing_poster: %w", err)
	}
	rep.MissingPoster = SeriesSignal{Count: posterCount, Items: posterItems}

	cutoffs := ports.StaleCutoffs{
		HotBefore:    now.Add(-uc.ttl.Hot),
		NormalBefore: now.Add(-uc.ttl.Normal),
		ColdBefore:   now.Add(-uc.ttl.Cold),
	}
	staleCount, staleItems, err := uc.repo.StaleEnrichment(ctx, cutoffs, drillDownLimit)
	if err != nil {
		return Report{}, fmt.Errorf("health build: stale_enrichment: %w", err)
	}
	rep.StaleEnrichment = StaleSignal{Count: staleCount, Items: staleItems}

	grabCount, grabItems, err := uc.repo.StuckGrabs(ctx, now.Add(-stuckGrabAge), drillDownLimit)
	if err != nil {
		return Report{}, fmt.Errorf("health build: stuck_grabs: %w", err)
	}
	rep.StuckGrabs = GrabSignal{Count: grabCount, Note: stuckGrabNote, Items: grabItems}

	deadCount, deadItems, err := uc.repo.DeadLetters(ctx, drillDownLimit)
	if err != nil {
		return Report{}, fmt.Errorf("health build: dead_letters: %w", err)
	}
	rep.DeadLetters = InboxSignal{Count: deadCount, Items: deadItems}

	rep.RateLimitPressure = DeferredSignal{
		Deferred: true,
		Reason:   rateLimitReason,
		Metric:   observability.MetricSonarrRateOversubscribed,
	}
	return rep, nil
}
