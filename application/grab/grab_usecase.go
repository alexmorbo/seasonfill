package grab

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	domaingrab "github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

// classifier separates transient (retry) from permanent (give-up) Sonarr errors.
type classifier interface {
	IsTransient(err error) bool
	Is4xx(err error) bool
}

// Sleeper is injected so tests can avoid wall-clock waits.
type Sleeper func(ctx context.Context, d time.Duration) error

// DefaultSleeper respects context cancellation.
func DefaultSleeper(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type Config struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	SeriesCooldown time.Duration
	GUIDCooldown   time.Duration
}

type UseCase struct {
	grabs     ports.GrabRepository
	cooldowns ports.CooldownRepository
	origins   ports.OriginReleaseRepository
	tx        ports.Transactor // optional (M-7); nil = direct writes
	classify  classifier
	sleep     Sleeper
	logger    *slog.Logger
	now       func() time.Time // injectable clock — defaults to time.Now().UTC()
}

func NewUseCase(
	grabs ports.GrabRepository,
	cooldowns ports.CooldownRepository,
	origins ports.OriginReleaseRepository,
	classify classifier,
	logger *slog.Logger,
) *UseCase {
	return &UseCase{
		grabs:     grabs,
		cooldowns: cooldowns,
		origins:   origins,
		classify:  classify,
		sleep:     DefaultSleeper,
		logger:    logger,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// WithClock swaps the time source — tests-only. Mirrors webhook.UseCase
// so application-layer tests share a single clock-injection pattern.
func (u *UseCase) WithClock(f func() time.Time) *UseCase { u.now = f; return u }

// WithTransactor wires the M-7 atomic-success-path transactor.
func (u *UseCase) WithTransactor(t ports.Transactor) *UseCase { u.tx = t; return u }

// WithSleeper swaps the sleeper for tests.
func (u *UseCase) WithSleeper(s Sleeper) *UseCase { u.sleep = s; return u }

type Input struct {
	ScanRunID    uuid.UUID
	InstanceName string
	SeriesID     int
	SeriesTitle  string
	SeasonNumber int
	Selected     release.Scored
	Coverage     int
	Sonarr       ports.SonarrClient
	Config       Config
}

type Output struct {
	Record   domaingrab.Record
	Attempts int
	Err      error
}

// Execute runs the retry loop, persists a grab_record, and activates the
// appropriate cooldown on success or final failure. On success the
// grab_record + series_cooldown + origin_release writes are executed inside
// a single DB transaction (M-7).
func (u *UseCase) Execute(ctx context.Context, in Input) Output {
	rec := domaingrab.Record{
		ID:                uuid.New(),
		InstanceName:      in.InstanceName,
		SeriesID:          in.SeriesID,
		SeriesTitle:       in.SeriesTitle,
		SeasonNumber:      in.SeasonNumber,
		ReleaseGUID:       in.Selected.Release.GUID,
		ReleaseTitle:      in.Selected.Release.Title,
		IndexerID:         in.Selected.Release.IndexerID,
		IndexerName:       in.Selected.Release.IndexerName,
		CustomFormatScore: in.Selected.Release.CustomFormatScore,
		Quality:           in.Selected.Release.QualityName,
		CoverageCount:     in.Coverage,
		ScanRunID:         in.ScanRunID,
		CreatedAt:         u.now(),
		UpdatedAt:         u.now(),
	}

	maxAttempts := in.Config.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		downloadID, err := in.Sonarr.ForceGrab(ctx, in.Selected.Release.GUID, in.Selected.Release.IndexerID)
		if err == nil {
			rec.Status = domaingrab.StatusGrabbed
			rec.Attempts = attempt
			rec.DownloadID = downloadID
			rec.UpdatedAt = u.now()
			u.persistSuccess(ctx, rec, in)
			observability.GrabRecorded(in.InstanceName, in.Selected.Release.IndexerName, "success")
			observability.GrabAttempt(in.InstanceName, "grabbed")
			u.logger.InfoContext(ctx, "grab_succeeded",
				slog.String("instance", in.InstanceName),
				slog.Int("series_id", in.SeriesID),
				slog.Int("season", in.SeasonNumber),
				slog.String("guid", in.Selected.Release.GUID),
				slog.String("indexer", in.Selected.Release.IndexerName),
				slog.String("download_id", downloadID),
				slog.Int("attempts", attempt),
			)
			return Output{Record: rec, Attempts: attempt}
		}

		lastErr = err

		if u.classify.Is4xx(err) {
			u.logger.ErrorContext(ctx, "grab_permanent_failure",
				slog.String("instance", in.InstanceName),
				slog.String("guid", in.Selected.Release.GUID),
				slog.String("error", err.Error()),
				slog.Int("attempt", attempt),
			)
			break
		}

		if !u.classify.IsTransient(err) {
			u.logger.ErrorContext(ctx, "grab_unclassified_failure",
				slog.String("instance", in.InstanceName),
				slog.String("guid", in.Selected.Release.GUID),
				slog.String("error", err.Error()),
				slog.Int("attempt", attempt),
			)
			break
		}

		if attempt < maxAttempts {
			d := backoffFor(attempt, in.Config.InitialBackoff, in.Config.MaxBackoff)
			if sleepErr := u.sleep(ctx, d); sleepErr != nil {
				lastErr = fmt.Errorf("context cancelled during backoff: %w", sleepErr)
				break
			}
			observability.GrabAttempt(in.InstanceName, "retried")
			u.logger.WarnContext(ctx, "grab_transient_retry",
				slog.String("instance", in.InstanceName),
				slog.String("guid", in.Selected.Release.GUID),
				slog.Int("attempt", attempt),
				slog.Duration("next_backoff", d),
				slog.String("error", err.Error()),
			)
			continue
		}
	}

	rec.Status = domaingrab.StatusGrabFailed
	rec.Attempts = attempts
	if lastErr != nil {
		rec.ErrorMessage = lastErr.Error()
	}
	rec.UpdatedAt = u.now()
	if persistErr := u.grabs.Create(ctx, rec); persistErr != nil {
		u.logger.ErrorContext(ctx, "persist grab_record failed",
			slog.String("error", persistErr.Error()),
			slog.String("guid", rec.ReleaseGUID),
		)
	}
	observability.GrabRecorded(in.InstanceName, in.Selected.Release.IndexerName, "failed")
	observability.GrabAttempt(in.InstanceName, "failed")
	u.activateGUIDCooldown(ctx, in, rec.ErrorMessage)
	return Output{Record: rec, Attempts: attempts, Err: fmt.Errorf("%w: %w", domain.ErrGrabFailed, lastErr)}
}

// persistSuccess wraps the three success-side writes in a single transaction
// when a Transactor is wired. Without one, the calls happen in sequence
// (003 behaviour preserved).
func (u *UseCase) persistSuccess(ctx context.Context, rec domaingrab.Record, in Input) {
	work := func(txCtx context.Context) error {
		if err := u.grabs.Create(txCtx, rec); err != nil {
			return fmt.Errorf("persist grab_record: %w", err)
		}
		if in.Config.SeriesCooldown > 0 {
			now := u.now()
			cd := cooldown.Cooldown{
				Scope:     cooldown.ScopeSeries,
				Key:       cooldown.SeriesKey(in.InstanceName, in.SeriesID, in.SeasonNumber),
				ExpiresAt: now.Add(in.Config.SeriesCooldown),
				Reason:    "series_after_grab",
				CreatedAt: now,
			}
			if err := u.cooldowns.Set(txCtx, cd); err != nil {
				return fmt.Errorf("set series cooldown: %w", err)
			}
		}
		if u.origins != nil {
			now := u.now()
			or := ports.OriginRelease{
				InstanceName: in.InstanceName,
				SeriesID:     in.SeriesID,
				SeasonNumber: in.SeasonNumber,
				GUID:         in.Selected.Release.GUID,
				IndexerID:    in.Selected.Release.IndexerID,
				IndexerName:  in.Selected.Release.IndexerName,
				Source:       "our_grab",
				FirstSeenAt:  now,
				LastSeenAt:   now,
				LastUsedAt:   &now,
			}
			if err := u.origins.Upsert(txCtx, or); err != nil {
				return fmt.Errorf("upsert origin_release: %w", err)
			}
		}
		return nil
	}

	var err error
	if u.tx != nil {
		err = u.tx.Transaction(ctx, work)
	} else {
		err = work(ctx)
	}
	if err != nil {
		u.logger.ErrorContext(ctx, "persist grab success-set failed",
			slog.String("instance", in.InstanceName),
			slog.String("guid", rec.ReleaseGUID),
			slog.String("error", err.Error()),
		)
	}
}

func (u *UseCase) activateGUIDCooldown(ctx context.Context, in Input, reason string) {
	if in.Config.GUIDCooldown <= 0 {
		return
	}
	now := u.now()
	if reason == "" {
		reason = "guid_after_failed_grab"
	}
	cd := cooldown.Cooldown{
		Scope:     cooldown.ScopeGUID,
		Key:       cooldown.GUIDKey(in.Selected.Release.GUID),
		ExpiresAt: now.Add(in.Config.GUIDCooldown),
		Reason:    reason,
		CreatedAt: now,
	}
	if err := u.cooldowns.Set(ctx, cd); err != nil {
		u.logger.ErrorContext(ctx, "set guid cooldown failed",
			slog.String("instance", in.InstanceName),
			slog.String("guid", in.Selected.Release.GUID),
			slog.String("error", err.Error()),
		)
	}
}

// IsGrabFailed reports whether the error from Execute is the wrapped
// domain.ErrGrabFailed sentinel.
func IsGrabFailed(err error) bool { return errors.Is(err, domain.ErrGrabFailed) }
