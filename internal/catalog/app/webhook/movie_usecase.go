package webhook

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieUseCase processes a Radarr webhook MovieEvent end-to-end: it drives the
// SAME movie_states + movies-canon cache writes the radarr-sync loop drives, via
// the SHARED scan.BuildRadarrMovieCache + scan.PersistRadarrMovieCache helpers
// (F-21). Mirror of the Sonarr webhook UseCase's handleSeriesAdd /
// handleSeriesDelete. Errors on the upsert path are WARN-logged and swallowed —
// Radarr retries on non-2xx and the next sync self-heals (D-2.5 sidecar rule).
type MovieUseCase struct {
	movies      scan.MovieCanonUpserter // enrichment MovieRepository (COALESCE Upsert)
	states      scan.MovieStateUpserter // THIN UpsertStub adapter (stat-preserving)
	softDeleter movieStateSoftDeleter
	logger      *slog.Logger
	now         func() time.Time
}

// movieStateSoftDeleter is the narrow soft-delete surface for MovieDelete.
type movieStateSoftDeleter interface {
	SoftDelete(ctx context.Context, instanceName domain.InstanceName, radarrMovieID int) error
}

// MovieDeps groups constructor params.
type MovieDeps struct {
	Movies      scan.MovieCanonUpserter
	States      scan.MovieStateUpserter // pass the THIN UpsertStub adapter
	SoftDeleter movieStateSoftDeleter
	Logger      *slog.Logger
}

func NewMovieUseCase(d MovieDeps) *MovieUseCase {
	lg := d.Logger
	if lg == nil {
		lg = sharedports.DomainLogger(slog.Default(), "webhook")
	}
	return &MovieUseCase{
		movies:      d.Movies,
		states:      d.States,
		softDeleter: d.SoftDeleter,
		logger:      lg,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// WithClock — tests only.
func (u *MovieUseCase) WithClock(f func() time.Time) *MovieUseCase { u.now = f; return u }

// Process consumes a domain MovieEvent. Returns nil on Unsupported events and
// (idempotent) already-deleted rows; non-nil only on real downstream failures
// so the drainer can retry (mirror of Process's transient-vs-terminal split).
func (u *MovieUseCase) Process(ctx context.Context, evt domainwebhook.MovieEvent) error {
	switch evt.Type {
	case domainwebhook.MovieEventTypeUnsupported:
		u.logger.DebugContext(ctx, "radarr_webhook_event_no_op",
			slog.String("instance", string(evt.InstanceName)),
			slog.String("raw_event_type", evt.RawEventType))
		return nil
	case domainwebhook.MovieEventTypeDeleted:
		return u.handleDelete(ctx, evt)
	case domainwebhook.MovieEventTypeUpsert:
		return u.handleUpsert(ctx, evt)
	default:
		u.logger.WarnContext(ctx, "radarr_webhook_event_unknown_type",
			slog.String("instance", string(evt.InstanceName)))
		return nil
	}
}

// handleUpsert normalises the event to ports.RadarrMovie, then drives the SHARED
// F-21 helpers — the exact same cache write the sync loop performs.
func (u *MovieUseCase) handleUpsert(ctx context.Context, evt domainwebhook.MovieEvent) error {
	if evt.RadarrMovieID == 0 {
		u.logger.DebugContext(ctx, "radarr_webhook_upsert_missing_id",
			slog.String("instance", string(evt.InstanceName)))
		return nil
	}
	// Normalise onto the SAME shape the GET /movie sync produces (F-21).
	m := ports.RadarrMovie{
		RadarrMovieID:       evt.RadarrMovieID,
		Title:               evt.Title,
		TitleSlug:           evt.TitleSlug,
		Year:                evt.Year,
		TMDBID:              evt.TMDBID,
		IMDBID:              evt.IMDBID,
		Monitored:           evt.Monitored,
		HasFile:             evt.HasFile,
		MinimumAvailability: evt.MinimumAvailability,
		// SizeOnDiskBytes intentionally 0: the webhook uses the THIN
		// UpsertStub writer which omits size, preserving the sync-written value.
	}
	cache := scan.BuildRadarrMovieCache(evt.InstanceName, m, u.now())
	if _, err := scan.PersistRadarrMovieCache(ctx, u.movies, u.states, cache); err != nil {
		u.logger.WarnContext(ctx, "radarr_webhook_upsert_failed",
			slog.String("instance", string(evt.InstanceName)),
			slog.Int("radarr_movie_id", evt.RadarrMovieID),
			slog.String("error", err.Error()))
		return nil // best-effort sidecar; next sync self-heals
	}
	u.logger.InfoContext(ctx, "radarr_webhook_movie_cached",
		slog.String("instance", string(evt.InstanceName)),
		slog.Int("radarr_movie_id", evt.RadarrMovieID),
		slog.String("raw_event_type", evt.RawEventType))
	return nil
}

// handleDelete soft-deletes the movie_states row (mirror handleSeriesDelete).
func (u *MovieUseCase) handleDelete(ctx context.Context, evt domainwebhook.MovieEvent) error {
	if u.softDeleter == nil || evt.RadarrMovieID == 0 {
		return nil
	}
	if err := u.softDeleter.SoftDelete(ctx, evt.InstanceName, evt.RadarrMovieID); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil // idempotent re-delivery
		}
		u.logger.WarnContext(ctx, "radarr_webhook_delete_failed",
			slog.String("instance", string(evt.InstanceName)),
			slog.Int("radarr_movie_id", evt.RadarrMovieID),
			slog.String("error", err.Error()))
		return nil
	}
	u.logger.InfoContext(ctx, "radarr_webhook_movie_deleted",
		slog.String("instance", string(evt.InstanceName)),
		slog.Int("radarr_movie_id", evt.RadarrMovieID))
	return nil
}
