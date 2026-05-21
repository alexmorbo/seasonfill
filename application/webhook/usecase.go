package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/webhook"
)

// GuidCooldownLookup returns the per-instance guid-after-failed-import
// cooldown duration. Built by the wiring layer (cmd/server/main.go)
// from cfg.SonarrInstances. A return value of 0 disables the cooldown
// write for that instance — used both for explicit opt-out and for
// graceful degradation when a webhook fires from an instance not in
// our config.
type GuidCooldownLookup func(instance string) time.Duration

// UseCase processes a Sonarr webhook event end-to-end: it looks up the
// matching grab_records row, transitions its status, and (on
// import_failed) adds a guid-scope cooldown. Status update and cooldown
// write are atomic via the injected Transactor.
type UseCase struct {
	grabs              ports.GrabRepository
	cooldowns          ports.CooldownRepository
	tx                 ports.Transactor
	guidCooldownLookup GuidCooldownLookup
	logger             *slog.Logger
	now                func() time.Time
}

// Deps groups constructor parameters.
type Deps struct {
	Grabs              ports.GrabRepository
	Cooldowns          ports.CooldownRepository
	Tx                 ports.Transactor
	GUIDCooldownLookup GuidCooldownLookup
	Logger             *slog.Logger
}

// New constructs a UseCase. Logger defaults to slog.Default().
// A nil GUIDCooldownLookup normalises to a closure returning 0 —
// same behaviour as the pre-008c cooldown-disabled path.
func New(d Deps) *UseCase {
	lg := d.Logger
	if lg == nil {
		lg = slog.Default()
	}
	lookup := d.GUIDCooldownLookup
	if lookup == nil {
		lookup = func(string) time.Duration { return 0 }
	}
	return &UseCase{
		grabs:              d.Grabs,
		cooldowns:          d.Cooldowns,
		tx:                 d.Tx,
		guidCooldownLookup: lookup,
		logger:             lg,
		now:                func() time.Time { return time.Now().UTC() },
	}
}

// WithClock swaps the time source — tests-only.
func (u *UseCase) WithClock(f func() time.Time) *UseCase { u.now = f; return u }

// Process consumes a domain.Event and applies it. Returns nil on
// Unsupported/Grabbed events, orphan events, and already-terminal rows
// (idempotent re-delivery). Returns non-nil only on real downstream
// failures (DB unavailable, transactor error).
func (u *UseCase) Process(ctx context.Context, evt webhook.Event) error {
	switch evt.Type {
	case webhook.EventTypeUnsupported, webhook.EventTypeGrabbed:
		u.logger.DebugContext(ctx, "webhook_event_no_op",
			slog.String("event_type", string(evt.Type)),
			slog.String("instance", evt.InstanceName),
			slog.String("raw_event_type", evt.RawEventType),
		)
		return nil
	case webhook.EventTypeImported, webhook.EventTypeImportFailed:
		// fall through
	default:
		u.logger.WarnContext(ctx, "webhook_event_unknown_type",
			slog.String("event_type", string(evt.Type)),
			slog.String("instance", evt.InstanceName),
		)
		return nil
	}

	target := mapEventToStatus(evt.Type)

	key := ports.MatchKey{
		DownloadID:   evt.DownloadID,
		ReleaseTitle: evt.ReleaseTitle,
		SeriesID:     evt.SeriesID,
		SeasonNumber: evt.SeasonNumber,
		InstanceName: evt.InstanceName,
	}

	rec, err := u.grabs.MatchLatest(ctx, key)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			u.logger.InfoContext(ctx, "webhook_orphan_event",
				slog.String("instance", evt.InstanceName),
				slog.String("event_type", string(evt.Type)),
				slog.String("download_id", evt.DownloadID),
				slog.String("release_title", evt.ReleaseTitle),
				slog.Int("series_id", evt.SeriesID),
				slog.Int("season", evt.SeasonNumber),
				slog.String("raw_event_type", evt.RawEventType),
			)
			return nil
		}
		// Wrap raw repo error with ErrDBUnavailable so 007c's
		// IsTransient classifier maps it to HTTP 500 (Sonarr retries).
		// ErrNotFound is handled above; everything else here is
		// driver-level (connection refused, query timeout, etc.).
		return fmt.Errorf("match grab record: %w: %w", ports.ErrDBUnavailable, err)
	}

	if !rec.Status.CanTransitionTo(target) {
		u.logger.DebugContext(ctx, "webhook_event_idempotent_skip",
			slog.String("instance", evt.InstanceName),
			slog.String("event_type", string(evt.Type)),
			slog.String("current_status", string(rec.Status)),
			slog.String("target_status", string(target)),
			slog.String("grab_id", rec.ID.String()),
			slog.String("raw_event_type", evt.RawEventType),
		)
		return nil
	}

	if target == grab.StatusImportFailed && u.guidCooldownLookup(evt.InstanceName) == 0 {
		u.logger.WarnContext(ctx, "webhook_unknown_instance_no_cooldown",
			slog.String("instance", evt.InstanceName),
			slog.String("event_type", string(evt.Type)),
			slog.String("grab_id", rec.ID.String()),
			slog.String("reason", "lookup_returned_zero_or_unconfigured"),
		)
	}

	work := func(txCtx context.Context) error {
		if err := u.grabs.UpdateStatus(txCtx, rec.ID, target, evt.Message); err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		if target == grab.StatusImportFailed {
			if dur := u.guidCooldownLookup(evt.InstanceName); dur > 0 {
				cd := cooldown.Cooldown{
					Scope:     cooldown.ScopeGUID,
					Key:       cooldown.GUIDKey(rec.ReleaseGUID),
					ExpiresAt: evt.OccurredAt.Add(dur),
					Reason:    "guid_after_failed_import",
					CreatedAt: evt.OccurredAt.UTC(),
				}
				if err := u.cooldowns.Set(txCtx, cd); err != nil {
					return fmt.Errorf("set guid cooldown: %w", err)
				}
			}
		}
		return nil
	}

	var txErr error
	if u.tx != nil {
		txErr = u.tx.Transaction(ctx, work)
	} else {
		txErr = work(ctx)
	}
	if txErr != nil {
		u.logger.ErrorContext(ctx, "webhook_process_failed",
			slog.String("instance", evt.InstanceName),
			slog.String("event_type", string(evt.Type)),
			slog.String("grab_id", rec.ID.String()),
			slog.String("error", txErr.Error()),
		)
		// Preserve already-classified sentinels — ErrInvalidStatusTransition
		// from the repo's defence-in-depth check is a logic error and
		// must NOT be relabelled as transient. Everything else gets the
		// ErrDBUnavailable wrap so 007c routes it to HTTP 500.
		if errors.Is(txErr, grab.ErrInvalidStatusTransition) ||
			errors.Is(txErr, ports.ErrDBUnavailable) {
			return txErr
		}
		return fmt.Errorf("webhook transaction: %w: %w", ports.ErrDBUnavailable, txErr)
	}

	u.logger.InfoContext(ctx, "webhook_event_applied",
		slog.String("instance", evt.InstanceName),
		slog.String("event_type", string(evt.Type)),
		slog.String("status", string(target)),
		slog.String("grab_id", rec.ID.String()),
		slog.String("guid", rec.ReleaseGUID),
	)
	return nil
}

func mapEventToStatus(t webhook.EventType) grab.Status {
	switch t {
	case webhook.EventTypeImported:
		return grab.StatusImported
	case webhook.EventTypeImportFailed:
		return grab.StatusImportFailed
	default:
		return ""
	}
}
