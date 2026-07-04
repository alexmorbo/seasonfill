package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	catseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// defaultTVDBResolveCooldown is how long a genuine "not on TMDB" verdict
// suppresses re-attempts. Keeps the scan from hammering /find on every
// 6h cycle for legitimately-tmdb-less series. Retryable (transient) TMDB
// errors do NOT record a cooldown — they self-heal on the next scan.
const defaultTVDBResolveCooldown = 7 * 24 * time.Hour

// TVDBResolverSeriesRepo is the narrow canon-lookup + write surface the
// resolver needs. Bound to *persistence.SeriesRepository in wiring.
type TVDBResolverSeriesRepo interface {
	FindByExternalIDs(ctx context.Context, tmdbID *domain.TMDBID, tvdbID *domain.TVDBID, imdbID *domain.IMDBID) (catseries.Canon, error)
	Upsert(ctx context.Context, c catseries.Canon) (domain.SeriesID, error)
}

// TVDBResolverTMDB is the /find surface (FindByTVDB only). *tmdb.Client /
// the TMDBClientHolder satisfy it.
type TVDBResolverTMDB interface {
	FindByTVDB(ctx context.Context, tvdbID domain.TVDBID) (*tmdb.FindResponse, error)
}

// TVDBResolver resolves a Sonarr series that shipped a tvdb_id but no
// tmdb_id to its TMDB id (one /find round-trip, SWR-cached 24h at the
// client), stamps canon.tmdb_id, and enqueues full enrichment. Genuine
// not-found is journalled to enrichment_errors(source=tvdb_resolve) with
// a 7-day NextAttemptAt so the scan does not retry on every cycle.
//
// Every method is best-effort: it returns nil on ALL non-fatal paths
// (canon missing, TMDB not-ready, transient error) so a resolver failure
// never aborts the scan piggyback.
type TVDBResolver struct {
	series   TVDBResolverSeriesRepo
	tmdb     TVDBResolverTMDB
	errs     EnrichmentErrorRepo
	disp     Dispatcher
	clock    func() time.Time
	cooldown time.Duration
	log      *slog.Logger
}

// NewTVDBResolver builds the resolver. clock nil → time.Now UTC;
// cooldown <= 0 → defaultTVDBResolveCooldown; log nil → slog.Default.
func NewTVDBResolver(
	series TVDBResolverSeriesRepo,
	tmdbFinder TVDBResolverTMDB,
	errs EnrichmentErrorRepo,
	disp Dispatcher,
	clock func() time.Time,
	cooldown time.Duration,
	log *slog.Logger,
) *TVDBResolver {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if cooldown <= 0 {
		cooldown = defaultTVDBResolveCooldown
	}
	if log == nil {
		log = slog.Default()
	}
	return &TVDBResolver{
		series: series, tmdb: tmdbFinder, errs: errs, disp: disp,
		clock: clock, cooldown: cooldown, log: log,
	}
}

// ResolveMissingTMDBID satisfies scan.TMDBResolver structurally. The scan
// caller guards on TMDBID==nil && TVDBID!=nil before calling; this method
// re-checks canon defensively (another path may have resolved it since).
func (r *TVDBResolver) ResolveMissingTMDBID(ctx context.Context, tvdbID domain.TVDBID) error {
	if r == nil || r.series == nil || r.tmdb == nil {
		return nil
	}
	log := r.log.With(slog.String("domain", "enrichment"),
		slog.String("op", "tvdb_resolve"), slog.Int("tvdb_id", int(tvdbID)))

	tv := tvdbID
	canon, err := r.series.FindByExternalIDs(ctx, nil, &tv, nil)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil // canon row not created yet — nothing to resolve
		}
		log.DebugContext(ctx, "tvdb_resolve.canon_lookup_failed", slog.String("error", err.Error()))
		return nil
	}
	if canon.TMDBID != nil {
		return nil // already resolved (race with another path)
	}

	now := r.clock()
	prevAttempts := 0
	if row, gerr := r.errs.GetByEntitySource(ctx,
		enrichment.EntityTypeSeries, int64(canon.ID), enrichment.SourceTVDBResolve); gerr == nil {
		if row.NextAttemptAt != nil && now.Before(*row.NextAttemptAt) {
			return nil // still in cooldown — do NOT re-attempt
		}
		prevAttempts = row.Attempts
	}

	fr, err := r.tmdb.FindByTVDB(ctx, tvdbID)
	if err != nil {
		// Transient (TMDB not-ready / network). No cooldown — self-heals
		// next scan. Avoids a permanent skip when TMDB is briefly down.
		log.DebugContext(ctx, "tvdb_resolve.find_failed", slog.String("error", err.Error()))
		return nil
	}
	if fr == nil || len(fr.TVResults) == 0 {
		r.recordCooldown(ctx, canon.ID, tvdbID, prevAttempts+1, now, log)
		return nil
	}

	resolved := domain.TMDBID(fr.TVResults[0].ID)
	if resolved <= 0 {
		r.recordCooldown(ctx, canon.ID, tvdbID, prevAttempts+1, now, log)
		return nil
	}

	// Collision guard: if another canon row already owns this tmdb_id,
	// stamping it here would violate the partial unique index on tmdb_id.
	// Enqueue that existing row for enrichment instead and clear our
	// cooldown — the show is resolvable, just under a different row.
	if existing, eerr := r.series.FindByExternalIDs(ctx, &resolved, nil, nil); eerr == nil && existing.ID != canon.ID {
		r.disp.Enqueue(EntitySeries, int64(existing.ID), PriorityCold)
		_ = r.errs.ClearOnSuccess(ctx, enrichment.EntityTypeSeries, int64(canon.ID), enrichment.SourceTVDBResolve)
		log.InfoContext(ctx, "tvdb_resolve.collision_enqueued_existing",
			slog.Int64("existing_series_id", int64(existing.ID)),
			slog.Int("tmdb_id", int(resolved)))
		return nil
	}

	canon.TMDBID = &resolved
	if _, uerr := r.series.Upsert(ctx, canon); uerr != nil {
		log.WarnContext(ctx, "tvdb_resolve.upsert_failed",
			slog.Int("tmdb_id", int(resolved)), slog.String("error", uerr.Error()))
		return nil
	}
	_ = r.errs.ClearOnSuccess(ctx, enrichment.EntityTypeSeries, int64(canon.ID), enrichment.SourceTVDBResolve)
	r.disp.Enqueue(EntitySeries, int64(canon.ID), PriorityCold)
	log.InfoContext(ctx, "tvdb_resolve.resolved",
		slog.Int64("series_id", int64(canon.ID)), slog.Int("tmdb_id", int(resolved)))
	return nil
}

func (r *TVDBResolver) recordCooldown(ctx context.Context, id domain.SeriesID, tvdbID domain.TVDBID, attempts int, now time.Time, log *slog.Logger) {
	next := now.Add(r.cooldown)
	rec := enrichment.EnrichmentError{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      int64(id),
		Source:        enrichment.SourceTVDBResolve,
		LastError:     fmt.Sprintf("tvdb_id %d not found on TMDB", int(tvdbID)),
		Attempts:      attempts,
		LastSeenAt:    now,
		NextAttemptAt: &next,
	}
	if err := r.errs.RecordFailure(ctx, rec); err != nil {
		log.WarnContext(ctx, "tvdb_resolve.record_cooldown_failed", slog.String("error", err.Error()))
		return
	}
	log.InfoContext(ctx, "tvdb_resolve.not_found_cooldown",
		slog.Int64("series_id", int64(id)), slog.Time("next_attempt_at", next))
}
