package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Dispatcher is the durable notification-outbox drainer (ADR-0016 N1, mirrors
// ADR-0005 webhook drainer). One background loop claims due pending rows FIFO,
// renders each, fans out to every enabled agent subscribed to the row's
// event_type via the Notifier, and settles the row: MarkSent on all-success,
// Reschedule(backoff) on any agent failure, MarkDead at the attempt ceiling.
type Dispatcher struct {
	outbox   ports.OutboxRepository
	agents   ports.NotificationAgentRepository
	notifier Notifier
	clock    clock.Clock
	logger   *slog.Logger

	tick       time.Duration
	claimLimit int
	attemptCap int
}

type DispatcherDeps struct {
	Outbox     ports.OutboxRepository
	Agents     ports.NotificationAgentRepository
	Notifier   Notifier
	Clock      clock.Clock // nil -> clock.Real()
	Logger     *slog.Logger
	Tick       time.Duration // default 5s
	ClaimLimit int           // default 50
	AttemptCap int           // default 10
}

const (
	defaultDispatchTick = 5 * time.Second
	defaultClaimLimit   = 50
	defaultAttemptCap   = 10
)

func NewDispatcher(d DispatcherDeps) *Dispatcher {
	clk := d.Clock
	if clk == nil {
		clk = clock.Real()
	}
	lg := d.Logger
	if lg == nil {
		lg = sharedports.DomainLogger(slog.Default(), "notification")
	}
	tick := d.Tick
	if tick <= 0 {
		tick = defaultDispatchTick
	}
	limit := d.ClaimLimit
	if limit <= 0 {
		limit = defaultClaimLimit
	}
	attemptCap := d.AttemptCap
	if attemptCap <= 0 {
		attemptCap = defaultAttemptCap
	}
	return &Dispatcher{
		outbox: d.Outbox, agents: d.Agents, notifier: d.Notifier,
		clock: clk, logger: lg, tick: tick, claimLimit: limit, attemptCap: attemptCap,
	}
}

// RunForever blocks until ctx is cancelled. Ticker-driven, immediate first pass.
func (d *Dispatcher) RunForever(ctx context.Context) {
	d.logger.InfoContext(ctx, "notification_dispatcher_started",
		slog.Duration("tick", d.tick), slog.Int("claim_limit", d.claimLimit),
		slog.Int("attempt_cap", d.attemptCap))
	t := d.clock.NewTicker(d.tick)
	defer t.Stop()
	d.dispatchOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			d.logger.InfoContext(ctx, "notification_dispatcher_stopped")
			return
		case <-t.C():
			d.dispatchOnce(ctx)
		}
	}
}

func (d *Dispatcher) dispatchOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	rows, err := d.outbox.FetchDueBatch(ctx, d.clock.Now(), d.claimLimit)
	if err != nil {
		d.logger.WarnContext(ctx, "notification_fetch_due_failed", slog.String("error", err.Error()))
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		d.dispatchRow(ctx, row)
	}
}

func (d *Dispatcher) dispatchRow(ctx context.Context, row ports.OutboxRow) {
	log := d.logger.With(slog.Int64("outbox_id", row.ID),
		slog.String("event_type", row.EventType), slog.Int("attempt", row.Attempts+1))

	subs, err := d.agents.ListEnabledForEvent(ctx, row.EventType)
	if err != nil {
		d.reschedule(ctx, row, log, "list agents: "+err.Error())
		return
	}
	if len(subs) == 0 {
		// Nobody subscribed — nothing to deliver. Drop the row (success).
		if err := d.outbox.MarkSent(ctx, row.ID); err != nil {
			log.WarnContext(ctx, "notification_mark_sent_failed", slog.String("error", err.Error()))
		}
		return
	}

	msg := Render(row.EventType, row.Payload)
	var anyErr bool
	for _, a := range subs {
		if serr := d.notifier.Send(ctx, a.ConfigEncrypted, msg); serr != nil {
			anyErr = true
			log.WarnContext(ctx, "notification_agent_send_failed",
				slog.Int64("agent_id", a.ID), slog.String("agent", a.Name),
				slog.String("error", serr.Error())) // no URL
		}
	}
	if !anyErr {
		if err := d.outbox.MarkSent(ctx, row.ID); err != nil {
			log.WarnContext(ctx, "notification_mark_sent_failed", slog.String("error", err.Error()))
			return
		}
		log.InfoContext(ctx, "notification_sent", slog.Int("agents", len(subs)))
		return
	}
	d.reschedule(ctx, row, log, "one or more agents failed")
}

func (d *Dispatcher) reschedule(ctx context.Context, row ports.OutboxRow, log *slog.Logger, reason string) {
	if row.Attempts+1 >= d.attemptCap {
		if err := d.outbox.MarkDead(ctx, row.ID); err != nil {
			log.ErrorContext(ctx, "notification_mark_dead_failed", slog.String("error", err.Error()))
			return
		}
		log.ErrorContext(ctx, "notification_dead_letter", slog.String("reason", reason))
		return
	}
	next := d.clock.Now().Add(backoffFor(row.Attempts + 1))
	if err := d.outbox.Reschedule(ctx, row.ID, next); err != nil {
		log.ErrorContext(ctx, "notification_reschedule_failed", slog.String("error", err.Error()))
		return
	}
	log.WarnContext(ctx, "notification_retry_scheduled",
		slog.String("reason", reason), slog.Time("next_attempt_at", next))
}
