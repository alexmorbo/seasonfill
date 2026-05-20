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
// Implemented in infrastructure/sonarr/errors.go but consumed here only via
// sentinel `errors.Is` checks against domain sentinels.
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
	classify  classifier
	sleep     Sleeper
	logger    *slog.Logger
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
	}
}

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
// appropriate cooldown on success or final failure. The returned Output
// always carries a Record (status=grabbed or grab_failed). When the loop
// exhausts retries Err is wrapped domain.ErrGrabFailed.
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
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}

	maxAttempts := in.Config.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		err := in.Sonarr.ForceGrab(ctx, in.Selected.Release.GUID, in.Selected.Release.IndexerID)
		if err == nil {
			rec.Status = domaingrab.StatusGrabbed
			rec.Attempts = attempt
			rec.UpdatedAt = time.Now().UTC()
			if persistErr := u.grabs.Create(ctx, rec); persistErr != nil {
				u.logger.ErrorContext(ctx, "persist grab_record failed",
					slog.String("error", persistErr.Error()),
					slog.String("guid", rec.ReleaseGUID),
				)
			}
			observability.GrabRecorded(in.InstanceName, in.Selected.Release.IndexerName, "success")
			observability.GrabAttempt(in.InstanceName, "grabbed")
			u.activateSeriesCooldown(ctx, in)
			u.upsertOrigin(ctx, in)
			u.logger.InfoContext(ctx, "grab_succeeded",
				slog.String("instance", in.InstanceName),
				slog.Int("series_id", in.SeriesID),
				slog.Int("season", in.SeasonNumber),
				slog.String("guid", in.Selected.Release.GUID),
				slog.String("indexer", in.Selected.Release.IndexerName),
				slog.Int("attempts", attempt),
			)
			return Output{Record: rec, Attempts: attempt}
		}

		lastErr = err

		// 4xx is configuration/bug — never retry.
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
			// Unknown classification — be conservative, do not retry.
			u.logger.ErrorContext(ctx, "grab_unclassified_failure",
				slog.String("instance", in.InstanceName),
				slog.String("guid", in.Selected.Release.GUID),
				slog.String("error", err.Error()),
				slog.Int("attempt", attempt),
			)
			break
		}

		// Transient — sleep and retry if attempts remain.
		if attempt < maxAttempts {
			d := backoffFor(attempt, in.Config.MaxBackoff)
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
	rec.UpdatedAt = time.Now().UTC()
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

func (u *UseCase) activateSeriesCooldown(ctx context.Context, in Input) {
	if in.Config.SeriesCooldown <= 0 {
		return
	}
	now := time.Now().UTC()
	cd := cooldown.Cooldown{
		Scope:     cooldown.ScopeSeries,
		Key:       cooldown.SeriesKey(in.InstanceName, in.SeriesID, in.SeasonNumber),
		ExpiresAt: now.Add(in.Config.SeriesCooldown),
		Reason:    "series_after_grab",
		CreatedAt: now,
	}
	if err := u.cooldowns.Set(ctx, cd); err != nil {
		u.logger.ErrorContext(ctx, "set series cooldown failed",
			slog.String("instance", in.InstanceName),
			slog.Int("series_id", in.SeriesID),
			slog.String("error", err.Error()),
		)
	}
}

func (u *UseCase) activateGUIDCooldown(ctx context.Context, in Input, reason string) {
	if in.Config.GUIDCooldown <= 0 {
		return
	}
	now := time.Now().UTC()
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

func (u *UseCase) upsertOrigin(ctx context.Context, in Input) {
	if u.origins == nil {
		return
	}
	now := time.Now().UTC()
	rec := ports.OriginRelease{
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
	if err := u.origins.Upsert(ctx, rec); err != nil {
		u.logger.ErrorContext(ctx, "upsert origin_release failed",
			slog.String("instance", in.InstanceName),
			slog.Int("series_id", in.SeriesID),
			slog.String("error", err.Error()),
		)
	}
}

// IsGrabFailed reports whether the error from Execute is the wrapped
// domain.ErrGrabFailed sentinel.
func IsGrabFailed(err error) bool { return errors.Is(err, domain.ErrGrabFailed) }
