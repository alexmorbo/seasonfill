package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Drainer is the durable-inbox FIFO drainer (ADR 0005, E3). A single
// background loop reclaims stale leases, claims due rows FIFO, re-maps
// the stored raw Sonarr body, and runs the existing Process under a
// per-job timeout. Failures are classified transient (retry with
// escalating backoff) vs logic/ceiling (dead-letter). See DEVIATIONS 1-6
// in the story.
//
// Concurrency model per ADR Decision 4: one global loop, per-job
// timeout, no worker pool (webhook volume is low). replicaCount=1 plus
// the SKIP-LOCKED-free conditional claim make a maxSurge overlap safe.
type Drainer struct {
	inbox    ports.WebhookInboxRepository
	process  ProcessFunc
	mapEvent MapFunc
	clock    clock.Clock
	logger   *slog.Logger
	pending  PendingDepthCounter // optional; nil disables the depth gauge
	// outbox + tx are the ADR-0016 N2.5 nil-OK transactional-outbox pair.
	// On dead-letter, markDead emits an inbox.dead_letter row (with a
	// dedup_key so a cascade collapses to one ping) in the SAME tx as
	// MarkDead when tx is wired, else best-effort after MarkDead.
	outbox ports.OutboxEmitter
	tx     ports.Transactor

	tick          time.Duration
	claimLimit    int
	perJobTimeout time.Duration
	leaseTTL      time.Duration
	attemptCap    int

	// Ф6-R-4b: radarr type-routing. All nil ⇒ every row drains via the sonarr
	// map+process — the existing behaviour, byte-identical.
	instanceType   func(name string) string
	radarrMapEvent func(payload []byte, instance domain.InstanceName) (webhook.MovieEvent, error)
	radarrProcess  func(ctx context.Context, evt webhook.MovieEvent) error

	poke chan struct{}
}

// ProcessFunc wraps webhook.UseCase.Process so the drainer is unit
// testable with a stub. webhook.Event is the DOMAIN type (see
// DEVIATION 6).
type ProcessFunc func(ctx context.Context, evt webhook.Event) error

// MapFunc re-maps a stored raw Sonarr body onto a domain Event. Defaults
// to sonarr.MapWebhookEvent; overridable in tests.
type MapFunc func(payload []byte, instance domain.InstanceName) (webhook.Event, error)

// PendingDepthCounter is the optional source for the pending-depth gauge.
// Satisfied by *catalogpersistence.WebhookInboxRepository via its
// concrete CountPending method (NOT part of the E2 port — DEVIATION 3).
type PendingDepthCounter interface {
	CountPending(ctx context.Context) (int64, error)
}

// Drainer defaults. Hardcoded now; env-config is E4.
const (
	defaultDrainTick     = 2 * time.Second
	defaultClaimLimit    = 50
	defaultPerJobTimeout = 30 * time.Second
	defaultAttemptCap    = 12
	minLeaseTTL          = 60 * time.Second
)

// DrainerDeps groups constructor parameters. Inbox + Process are
// required; everything else has a sane default.
type DrainerDeps struct {
	Inbox   ports.WebhookInboxRepository
	Process ProcessFunc
	// MapEvent overrides the mapper (tests). Nil -> sonarr.MapWebhookEvent.
	MapEvent MapFunc
	// Clock nil -> clock.Real().
	Clock clock.Clock
	// Logger nil -> DomainLogger(slog.Default(), "webhook"). "webhook" is
	// the PRD §6.5 allowed domain shared with the rest of webhook
	// processing; "webhook_inbox" is NOT in the closed AllowedDomains list.
	Logger *slog.Logger
	// PendingCounter is optional; nil disables the pending-depth gauge.
	PendingCounter PendingDepthCounter
	// Outbox is the ADR-0016 notification emitter (nil-OK). Wired, markDead
	// emits an inbox.dead_letter row.
	Outbox ports.OutboxEmitter
	// Tx wraps MarkDead + the outbox Insert in one tx (nil-OK — best-effort
	// emit after MarkDead when absent).
	Tx ports.Transactor

	Tick          time.Duration // default 2s
	ClaimLimit    int           // default 50
	PerJobTimeout time.Duration // default 30s
	AttemptCap    int           // default 12
	// LeaseTTL default = max(2*PerJobTimeout, 60s). F-13: must be
	// >= PerJobTimeout so a full-timeout job keeps a live lease.
	LeaseTTL time.Duration

	// InstanceTypeResolver returns the arr_instance.type for an instance name
	// ("sonarr" | "radarr"). Nil (default) ⇒ every row drains via the sonarr
	// map+process — the existing behaviour, byte-identical. Ф6-R-4b.
	InstanceTypeResolver func(name string) string
	// RadarrMapEvent / RadarrProcess are the radarr-side map + unit-of-work,
	// used only when InstanceTypeResolver reports "radarr". Nil ⇒ radarr rows
	// fall through to the sonarr path (which classifies them Unsupported).
	RadarrMapEvent func(payload []byte, instance domain.InstanceName) (webhook.MovieEvent, error)
	RadarrProcess  func(ctx context.Context, evt webhook.MovieEvent) error
}

// NewDrainer constructs a Drainer, applying defaults.
func NewDrainer(d DrainerDeps) *Drainer {
	lg := d.Logger
	if lg == nil {
		// "webhook" (not "webhook_inbox") — the latter is not in the PRD
		// §6.5 AllowedDomains closed list, so DomainLogger would panic.
		lg = sharedports.DomainLogger(slog.Default(), "webhook")
	}
	mp := d.MapEvent
	if mp == nil {
		mp = sonarr.MapWebhookEvent
	}
	clk := d.Clock
	if clk == nil {
		clk = clock.Real()
	}
	tick := d.Tick
	if tick <= 0 {
		tick = defaultDrainTick
	}
	limit := d.ClaimLimit
	if limit <= 0 {
		limit = defaultClaimLimit
	}
	perJob := d.PerJobTimeout
	if perJob <= 0 {
		perJob = defaultPerJobTimeout
	}
	cap := d.AttemptCap
	if cap <= 0 {
		cap = defaultAttemptCap
	}
	lease := d.LeaseTTL
	if lease < perJob { // F-13 invariant
		lease = 2 * perJob
	}
	if lease < minLeaseTTL {
		lease = minLeaseTTL
	}
	return &Drainer{
		inbox:         d.Inbox,
		process:       d.Process,
		mapEvent:      mp,
		clock:         clk,
		logger:        lg,
		pending:       d.PendingCounter,
		outbox:        d.Outbox,
		tx:            d.Tx,
		tick:          tick,
		claimLimit:    limit,
		perJobTimeout: perJob,
		leaseTTL:      lease,
		attemptCap:    cap,
		// Ф6-R-4b: straight assignment, no defaulting. Nil ⇒ sonarr-only drain.
		instanceType:   d.InstanceTypeResolver,
		radarrMapEvent: d.RadarrMapEvent,
		radarrProcess:  d.RadarrProcess,
		poke:           make(chan struct{}, 1),
	}
}

// RunForever blocks until ctx is cancelled. Ticker (durability backbone)
// + poke channel (latency optimisation) drive drainOnce. Matches the
// lifecycle.Go fn signature. Graceful shutdown = ctx cancel: an
// in-flight Process observes the derived job ctx cancel and the row is
// left leased for ReclaimStale on the next boot (no spurious failure
// write).
func (d *Drainer) RunForever(ctx context.Context) {
	d.logger.InfoContext(ctx, "webhook_inbox_drainer_started",
		slog.Duration("tick", d.tick),
		slog.Int("claim_limit", d.claimLimit),
		slog.Duration("per_job_timeout", d.perJobTimeout),
		slog.Duration("lease_ttl", d.leaseTTL),
		slog.Int("attempt_cap", d.attemptCap),
	)
	ticker := d.clock.NewTicker(d.tick)
	defer ticker.Stop()

	d.drainOnce(ctx) // immediate first pass so a pending backlog on boot drains now
	for {
		select {
		case <-ctx.Done():
			d.logger.InfoContext(ctx, "webhook_inbox_drainer_stopped")
			return
		case <-ticker.C():
			d.drainOnce(ctx)
		case <-d.poke:
			d.drainOnce(ctx)
		}
	}
}

// Poke requests an early drain. Non-blocking: a full buffer means a
// drain is already queued, so losing this signal is safe (the ticker
// catches up). Exposed for E4 (post-insert latency optimisation).
func (d *Drainer) Poke() {
	select {
	case d.poke <- struct{}{}:
	default:
	}
}

// drainOnce reclaims stale leases, then claims+processes due rows until
// none remain. Termination: every claimed row is either deleted
// (success) or pushed into the future (MarkFailure/MarkDead), so a
// subsequent ClaimDue with the same virtual `now` eventually returns
// empty.
//
// F-15 (best-effort FIFO): ordering is best-effort only — a backing-off
// failed row is skipped (its next_attempt_at is in the future) while
// later due rows proceed, so strict global ordering is NOT guaranteed.
func (d *Drainer) drainOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if n, err := d.inbox.ReclaimStale(ctx, d.clock.Now()); err != nil {
		d.logger.WarnContext(ctx, "webhook_inbox_reclaim_failed",
			slog.String("error", err.Error()))
	} else if n > 0 {
		d.logger.InfoContext(ctx, "webhook_inbox_reclaimed_stale",
			slog.Int64("count", n))
	}

	// succeeded tracks dedup keys that succeeded THIS pass (F-14).
	succeeded := make(map[string]struct{})
	for {
		if ctx.Err() != nil {
			return
		}
		now := d.clock.Now()
		leaseUntil := now.Add(d.leaseTTL)
		rows, err := d.inbox.ClaimDue(ctx, now, leaseUntil, d.claimLimit)
		if err != nil {
			d.logger.WarnContext(ctx, "webhook_inbox_claim_failed",
				slog.String("error", err.Error()))
			break
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			d.processRow(ctx, row, succeeded)
		}
	}

	d.updatePendingGauge(ctx)
}

// processRow drains one claimed row.
func (d *Drainer) processRow(ctx context.Context, row ports.WebhookInboxRow, succeeded map[string]struct{}) {
	// Ф6-R-4b: route radarr-instance rows to the radarr map+process. Nil
	// resolver / nil radarr hooks ⇒ fall through to the sonarr path unchanged.
	if d.instanceType != nil && d.radarrProcess != nil &&
		d.instanceType(row.InstanceName) == scan.InstanceTypeRadarr {
		d.processRadarrRow(ctx, row, succeeded)
		return
	}

	log := d.logger.With(
		slog.Int64("inbox_id", row.ID),
		slog.Int("attempt", row.Attempts+1),
		slog.String("instance", row.InstanceName),
		slog.String("event_type", row.EventType),
	)

	evt, err := d.mapEvent(row.Payload, domain.InstanceName(row.InstanceName))
	if err != nil {
		// A stored body that no longer maps is a permanently-bad payload
		// (never retry a malformed body). Dead-letter for forensics.
		log.ErrorContext(ctx, "webhook_inbox_map_failed", slog.String("error", err.Error()))
		d.markDead(ctx, row.ID, err, log)
		return
	}

	// F-14: skip the outbound-heavy Process if an identical event already
	// succeeded in this drain pass (a duplicate re-delivery). Cross-pass
	// duplicates fall through to Process's own CanTransitionTo idempotency.
	key := dedupKey(evt)
	if _, dup := succeeded[key]; dup {
		log.InfoContext(ctx, "webhook_inbox_duplicate_skipped")
		d.markSuccess(ctx, row.ID, log)
		observability.IncWebhookInboxOutcome("success")
		return
	}

	jobCtx, cancel := d.withJobTimeout(ctx)
	perr := d.process(jobCtx, evt)
	cancel()

	// Shutdown discrimination (DEVIATION 5 / F-13): a cancel while the
	// PARENT ctx is done is shutdown, not a per-job timeout. Leave the row
	// in processing (leased) so ReclaimStale recovers it on the next boot;
	// do NOT write a failure.
	if ctx.Err() != nil {
		log.InfoContext(ctx, "webhook_inbox_drain_interrupted")
		return
	}

	if perr == nil {
		d.markSuccess(ctx, row.ID, log)
		observability.IncWebhookInboxOutcome("success")
		succeeded[key] = struct{}{}
		return
	}

	if d.isRetryable(perr) && row.Attempts+1 < d.attemptCap {
		next := d.clock.Now().Add(backoffFor(row.Attempts + 1))
		if err := d.inbox.MarkFailure(ctx, row.ID, perr.Error(), next); err != nil {
			log.ErrorContext(ctx, "webhook_inbox_mark_failure_failed", slog.String("error", err.Error()))
			return
		}
		observability.IncWebhookInboxOutcome("retry")
		log.WarnContext(ctx, "webhook_inbox_retry_scheduled",
			slog.String("error", perr.Error()),
			slog.Time("next_attempt_at", next),
		)
		return
	}

	// Retryable-but-ceiling, or a non-retryable logic error -> dead-letter.
	d.markDead(ctx, row.ID, perr, log)
}

// processRadarrRow drains one claimed radarr-instance row. Mirror of processRow
// with the radarr map/process + a movie dedup key. Reuses the SAME markSuccess /
// markDead / isRetryable / withJobTimeout helpers so retry/dead-letter semantics
// are identical to the sonarr path. Ф6-R-4b.
func (d *Drainer) processRadarrRow(ctx context.Context, row ports.WebhookInboxRow, succeeded map[string]struct{}) {
	log := d.logger.With(
		slog.Int64("inbox_id", row.ID),
		slog.Int("attempt", row.Attempts+1),
		slog.String("instance", row.InstanceName),
		slog.String("event_type", row.EventType),
		slog.String("vertical", "radarr"),
	)
	evt, err := d.radarrMapEvent(row.Payload, domain.InstanceName(row.InstanceName))
	if err != nil {
		log.ErrorContext(ctx, "radarr_webhook_inbox_map_failed", slog.String("error", err.Error()))
		d.markDead(ctx, row.ID, err, log)
		return
	}
	key := radarrDedupKey(evt)
	if _, dup := succeeded[key]; dup {
		log.InfoContext(ctx, "radarr_webhook_inbox_duplicate_skipped")
		d.markSuccess(ctx, row.ID, log)
		observability.IncWebhookInboxOutcome("success")
		return
	}
	jobCtx, cancel := d.withJobTimeout(ctx)
	perr := d.radarrProcess(jobCtx, evt)
	cancel()
	if ctx.Err() != nil {
		log.InfoContext(ctx, "radarr_webhook_inbox_drain_interrupted")
		return
	}
	if perr == nil {
		d.markSuccess(ctx, row.ID, log)
		observability.IncWebhookInboxOutcome("success")
		succeeded[key] = struct{}{}
		return
	}
	if d.isRetryable(perr) && row.Attempts+1 < d.attemptCap {
		next := d.clock.Now().Add(backoffFor(row.Attempts + 1))
		if merr := d.inbox.MarkFailure(ctx, row.ID, perr.Error(), next); merr != nil {
			log.ErrorContext(ctx, "radarr_webhook_inbox_mark_failure_failed", slog.String("error", merr.Error()))
			return
		}
		observability.IncWebhookInboxOutcome("retry")
		return
	}
	d.markDead(ctx, row.ID, perr, log)
}

// radarrDedupKey is the F-14 same-pass identity of a movie event's cache effect.
func radarrDedupKey(evt webhook.MovieEvent) string {
	return string(evt.InstanceName) + "|radarr|" + evt.RawEventType + "|" + strconv.Itoa(evt.RadarrMovieID)
}

// withJobTimeout derives a per-job ctx cancelled after perJobTimeout,
// driven by the injected clock (so the fake clock can fire it
// deterministically — DEVIATION 5). A completed job stops the timer via
// jobCtx.Done().
func (d *Drainer) withJobTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if d.perJobTimeout <= 0 {
		return context.WithCancel(parent)
	}
	jobCtx, cancel := context.WithCancel(parent)
	timer := d.clock.NewTimer(d.perJobTimeout)
	go func() {
		select {
		case <-timer.C():
			cancel()
		case <-jobCtx.Done():
			timer.Stop()
		}
	}()
	return jobCtx, cancel
}

// isRetryable = webhook-transient (DB/deadline/canceled — reuses
// IsTransient, NOT reinvented) OR outbound-Sonarr-transient (5xx/408/429/
// network/timeout via sonarr.IsTransient, plus the ErrInstanceNetwork /
// ErrInstanceUnauthorized sentinels). See DEVIATION 1 — the outbound
// branch is dormant until Process propagates outbound errors.
func (d *Drainer) isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if IsTransient(err) { // same-package (app/webhook) helper
		return true
	}
	if sonarr.IsTransient(err) {
		return true
	}
	if errors.Is(err, sharedErrors.ErrInstanceNetwork) {
		return true
	}
	if errors.Is(err, sharedErrors.ErrInstanceUnauthorized) {
		return true
	}
	return false
}

func (d *Drainer) markSuccess(ctx context.Context, id int64, log *slog.Logger) {
	if err := d.inbox.MarkSuccess(ctx, id); err != nil {
		log.ErrorContext(ctx, "webhook_inbox_mark_success_failed", slog.String("error", err.Error()))
		return
	}
	log.InfoContext(ctx, "webhook_inbox_processed")
}

func (d *Drainer) markDead(ctx context.Context, id int64, cause error, log *slog.Logger) {
	// ADR-0016 N2.5: emit inbox.dead_letter with a per-inbox-id dedup_key so
	// a dead-letter cascade collapses to one ping. When a Transactor is
	// wired the MarkDead + Insert run in one tx; otherwise the emit is
	// best-effort after MarkDead.
	emit := func(txCtx context.Context) error {
		if err := d.inbox.MarkDead(txCtx, id, cause.Error()); err != nil {
			return err
		}
		if d.outbox != nil {
			dk := fmt.Sprintf("inbox_dead:%d", id)
			payload, _ := json.Marshal(map[string]any{
				"inbox_id":   id,
				"event_type": "inbox.dead_letter",
			})
			return d.outbox.Insert(txCtx, ports.OutboxRow{
				EventType: "inbox.dead_letter",
				Payload:   payload,
				DedupKey:  &dk,
			})
		}
		return nil
	}
	var err error
	if d.tx != nil {
		err = d.tx.Transaction(ctx, emit)
	} else {
		err = emit(ctx)
	}
	if err != nil {
		log.ErrorContext(ctx, "webhook_inbox_mark_dead_failed", slog.String("error", err.Error()))
		return
	}
	observability.IncWebhookInboxOutcome("dead")
	observability.IncWebhookInboxDead()
	log.ErrorContext(ctx, "webhook_inbox_dead_letter", slog.String("error", cause.Error()))
}

func (d *Drainer) updatePendingGauge(ctx context.Context) {
	if d.pending == nil {
		return
	}
	n, err := d.pending.CountPending(ctx)
	if err != nil {
		d.logger.WarnContext(ctx, "webhook_inbox_pending_count_failed", slog.String("error", err.Error()))
		return
	}
	observability.SetWebhookInboxPending(float64(n))
}

// dedupKey is the F-14 same-pass identity of an event's outbound effect:
// the tuple that MatchLatest / refreshEpisodeStates key on. Two rows with
// the same key in one pass are the same delivery re-sent by Sonarr.
func dedupKey(evt webhook.Event) string {
	return string(evt.InstanceName) + "|" +
		evt.RawEventType + "|" +
		evt.DownloadID + "|" +
		strconv.Itoa(int(evt.SeriesID)) + "|" +
		strconv.Itoa(evt.SeasonNumber)
}
