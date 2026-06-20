// Package seriesrefresh is the application use case behind
// POST /api/v1/instances/:name/series/:id/refresh (story 218 E-2).
//
// One transaction-free path: resolve sonarrID → canon series.id via
// SeriesCacheRepository.Get, then enqueue (series, top-10 persons,
// omdb-if-imdb-id) at PriorityHot. The dispatcher (story 211) handles
// the actual TMDB/OMDb calls and the dedup contract.
package seriesrefresh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	enrichment "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Deps groups the consumer-side ports. SeriesPeople is OPTIONAL:
// when nil, the refresh enqueues only the series + (optional) OMDb.
type Deps struct {
	SeriesCache  ports.SeriesCacheRepository
	Series       SeriesByIDReader
	SeriesPeople TopCastReader
	Dispatcher   enrichment.Dispatcher
	Logger       *slog.Logger
}

// SeriesByIDReader is the minimal series-canon read surface — we need
// imdb_id to decide whether OMDb is on the menu.
type SeriesByIDReader interface {
	Get(ctx context.Context, id domain.SeriesID) (CanonView, error)
}

// CanonView is the trimmed projection — keeps this package free of
// the domain/series import.
type CanonView struct {
	ID     domain.SeriesID
	IMDBID *domain.IMDBID
}

// TopCastReader returns up to N person canon ids for a series. The
// composer's cast pipeline already implements the underlying read via
// SeriesPeopleRepository.ListBySeries — reuse via adapter in
// cmd/server.
type TopCastReader interface {
	TopCastPersonIDs(ctx context.Context, seriesID domain.SeriesID, limit int) ([]int64, error)
}

// Result is the handler-visible outcome.
type Result struct {
	SeriesID     domain.SeriesID
	SeriesQueued bool
	Persons      int
	OMDbQueued   bool
}

// UseCase is the entrypoint. Best-effort: every Enqueue may dedup
// against an already-in-flight job; we still report it as queued
// from the operator's perspective (the work IS scheduled, just not
// by this exact call).
type UseCase struct {
	deps Deps
	log  *slog.Logger
}

// New constructs a UseCase. Dispatcher MUST be non-nil; SeriesPeople
// is optional.
func New(d Deps) (*UseCase, error) {
	if d.SeriesCache == nil {
		return nil, errors.New("seriesrefresh: SeriesCache required")
	}
	if d.Series == nil {
		return nil, errors.New("seriesrefresh: Series required")
	}
	if d.Dispatcher == nil {
		return nil, errors.New("seriesrefresh: Dispatcher required")
	}
	lg := d.Logger
	if lg == nil {
		lg = sharedports.DomainLogger(slog.Default(), "composer")
	}
	return &UseCase{deps: d, log: lg}, nil
}

// Refresh resolves the cache row, enqueues series + (optional) cast
// + (optional) OMDb, returns the per-section counts. ports.ErrNotFound
// if the cache row is missing or has no canon series_id.
func (u *UseCase) Refresh(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (Result, error) {
	cache, err := u.deps.SeriesCache.Get(ctx, instanceName, sonarrSeriesID)
	if err != nil {
		return Result{}, fmt.Errorf("seriesrefresh: resolve cache: %w", err)
	}
	if cache.SeriesID == nil || *cache.SeriesID == 0 {
		return Result{}, fmt.Errorf("seriesrefresh: %w (cache row has no canon series_id)", ports.ErrNotFound)
	}
	seriesID := *cache.SeriesID

	res := Result{SeriesID: seriesID}

	u.deps.Dispatcher.Enqueue(enrichment.EntitySeries, int64(seriesID), enrichment.PriorityHot)
	res.SeriesQueued = true

	if u.deps.SeriesPeople != nil {
		const topN = 10
		ids, perr := u.deps.SeriesPeople.TopCastPersonIDs(ctx, seriesID, topN)
		if perr != nil {
			u.log.WarnContext(ctx, "seriesrefresh.top_cast_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("error", perr.Error()))
		}
		for _, pid := range ids {
			u.deps.Dispatcher.Enqueue(enrichment.EntityPerson, pid, enrichment.PriorityHot)
			res.Persons++
		}
	}

	canon, err := u.deps.Series.Get(ctx, seriesID)
	if err != nil {
		u.log.WarnContext(ctx, "seriesrefresh.canon_read_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("error", err.Error()))
	} else if canon.IMDBID != nil && *canon.IMDBID != "" {
		u.deps.Dispatcher.Enqueue(enrichment.EntityOMDb, int64(seriesID), enrichment.PriorityHot)
		res.OMDbQueued = true
	}

	u.log.InfoContext(ctx, "seriesrefresh.enqueued",
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrSeriesID)),
		slog.Int64("series_id", int64(seriesID)),
		slog.Int("persons", res.Persons),
		slog.Bool("omdb", res.OMDbQueued),
	)
	return res, nil
}
