// Package seriesdetail — see ports.go header.
//
// series_ratings.go (W18-7a). Stale-while-revalidate ratings usecase behind
// GET /series/:id/ratings. Per-source (TMDB, OMDb) state machine on the canon row:
// fresh ⇒ return; stale ⇒ return OLD value + kick BG refresh (single-flight);
// empty+id ⇒ blocking fetch ≤3s (single-flight); empty+no-id / empty-but-fresh ⇒
// unavailable. Both branches run in parallel under a shared 3s deadline. Background
// refresh uses a detached context (context.WithoutCancel) so it outlives the request.
// Never returns 5xx; the handler always answers 200 for fetch outcomes.
package seriesdetail

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// defaultRatingsFetchDeadline bounds the SYNCHRONOUS wait a viewer tolerates for a
// blocking (empty+id) fetch. On timeout the source reports `pending` and the
// single-flight refresh continues in the background. Overridable per-usecase via
// SeriesRatingsDeps.FetchDeadline (test-seam; keep the prod default).
const defaultRatingsFetchDeadline = 3 * time.Second

// defaultRatingsBackgroundTimeout bounds the DETACHED background refresh. Longer than
// the viewer deadline so a fetch that overran the 3s window still completes + persists.
const defaultRatingsBackgroundTimeout = 20 * time.Second

// TMDBRatingRefresher fetches + owner-writes a series' tmdb_rating. Satisfied by
// *RatingsTMDBRefresher. Returns nil on all terminal outcomes; the usecase re-reads
// the canon to observe whether a value landed.
type TMDBRatingRefresher interface {
	Refresh(ctx context.Context, seriesID domain.SeriesID) error
}

// OMDbRatingRefresher fetches + owner-writes a series' OMDb columns. Satisfied by
// *enrichment.OMDbWorker (Handle) — it already owns budget/terminal/TTL/owner-write/
// journal, so the usecase reuses it wholesale (no duplication).
type OMDbRatingRefresher interface {
	Handle(ctx context.Context, seriesID domain.SeriesID) error
}

// SeriesRatingsDeps — narrow ports. SeriesPort (Get) is the existing canon loader.
// FetchDeadline / BackgroundTimeout are optional (0 ⇒ prod defaults); they are a
// test-seam so the timeout / single-flight tests can shrink the viewer wait.
type SeriesRatingsDeps struct {
	Series            SeriesPort
	TMDB              TMDBRatingRefresher
	OMDb              OMDbRatingRefresher
	Logger            *slog.Logger
	Now               func() time.Time
	FetchDeadline     time.Duration
	BackgroundTimeout time.Duration
}

// SeriesRatingsUseCase serves the SWR ratings endpoint. The singleflight.Group is a
// long-lived field so concurrent requests + background kicks share one in-flight
// refresh per (series_id, source).
type SeriesRatingsUseCase struct {
	d  SeriesRatingsDeps
	sf singleflight.Group
}

// NewSeriesRatingsUseCase validates required deps. TMDB/OMDb refreshers are nil-OK:
// a nil refresher degrades that source to read-only (fresh/unavailable, never a
// fetch) — keeps the endpoint working in minimal boot / when a subsystem is disabled.
func NewSeriesRatingsUseCase(d SeriesRatingsDeps) (*SeriesRatingsUseCase, error) {
	if d.Series == nil {
		return nil, errors.New("series ratings: Series port required")
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.FetchDeadline <= 0 {
		d.FetchDeadline = defaultRatingsFetchDeadline
	}
	if d.BackgroundTimeout <= 0 {
		d.BackgroundTimeout = defaultRatingsBackgroundTimeout
	}
	return &SeriesRatingsUseCase{d: d}, nil
}

// GetRatings loads the canon row and resolves both sources in parallel under a shared
// deadline, returning the assembled DTO. ErrNotFound (unknown canon id) is propagated
// so the handler can 404; all fetch outcomes yield a 200 DTO.
func (u *SeriesRatingsUseCase) GetRatings(ctx context.Context, seriesID domain.SeriesID) (*dto.SeriesRatingsResponse, error) {
	canon, err := u.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, err // ports.ErrNotFound → handler 404
	}
	now := u.d.Now()

	// Shared deadline for any SYNCHRONOUS blocking fetch across BOTH branches.
	fetchCtx, cancel := context.WithTimeout(ctx, u.d.FetchDeadline)
	defer cancel()

	var (
		tmdbStatus string
		omdbStatus string
		// mutated canon after a blocking reload (per source)
		tmdbCanon = canon
		omdbCanon = canon
	)

	done := make(chan struct{}, 2)
	go func() {
		tmdbStatus, tmdbCanon = u.resolveTMDB(ctx, fetchCtx, seriesID, canon, now)
		done <- struct{}{}
	}()
	go func() {
		omdbStatus, omdbCanon = u.resolveOMDb(ctx, fetchCtx, seriesID, canon, now)
		done <- struct{}{}
	}()
	<-done
	<-done

	resp := &dto.SeriesRatingsResponse{
		TMDBRating: tmdbCanon.TMDBRating,
		TMDBVotes:  tmdbCanon.TMDBVotes,
		IMDBRating: omdbCanon.IMDBRating,
		IMDBVotes:  omdbCanon.IMDBVotes,
		Rated:      omdbCanon.OMDBRated,
		Awards:     omdbCanon.OMDBAwards,
		Sources:    dto.SeriesRatingsSources{TMDB: tmdbStatus, OMDb: omdbStatus},
	}
	return resp, nil
}

// --- TMDB branch -----------------------------------------------------------

func (u *SeriesRatingsUseCase) resolveTMDB(reqCtx, fetchCtx context.Context, seriesID domain.SeriesID, canon series.Canon, now time.Time) (string, series.Canon) {
	hasID := canon.TMDBID != nil && int64(*canon.TMDBID) != 0
	hasValue := canon.TMDBRating != nil
	stale := TMDBRatingStale(now, canon.EnrichmentTMDBSyncedAt, canon.InProduction, canon.Status, canon.LastAirDate, canon.FirstAirDate)

	if u.d.TMDB == nil {
		hasID = false // no refresher ⇒ read-only: fresh (if value) or unavailable
	}
	key := "tmdb:" + strconv.FormatInt(int64(seriesID), 10)
	refresh := func(ctx context.Context) error { return u.d.TMDB.Refresh(ctx, seriesID) }
	reload := func(ctx context.Context) (series.Canon, bool) {
		c, err := u.d.Series.Get(ctx, seriesID)
		return c, err == nil
	}
	valuePresent := func(c series.Canon) bool { return c.TMDBRating != nil }
	return u.resolveSource(reqCtx, fetchCtx, key, hasID, hasValue, stale, canon, refresh, reload, valuePresent)
}

// --- OMDb branch -----------------------------------------------------------

func (u *SeriesRatingsUseCase) resolveOMDb(reqCtx, fetchCtx context.Context, seriesID domain.SeriesID, canon series.Canon, now time.Time) (string, series.Canon) {
	hasID := canon.IMDBID != nil && *canon.IMDBID != ""
	hasValue := canon.IMDBRating != nil
	stale := OMDbRatingStale(now, canon.EnrichmentOMDBSyncedAt, canon.InProduction, canon.Status, canon.LastAirDate, canon.FirstAirDate)

	if u.d.OMDb == nil {
		hasID = false
	}
	key := "omdb:" + strconv.FormatInt(int64(seriesID), 10)
	// OMDb fetch = the shipped worker (budget/terminal/TTL/owner-write/journal).
	refresh := func(ctx context.Context) error { return u.d.OMDb.Handle(ctx, seriesID) }
	reload := func(ctx context.Context) (series.Canon, bool) {
		c, err := u.d.Series.Get(ctx, seriesID)
		return c, err == nil
	}
	valuePresent := func(c series.Canon) bool { return c.IMDBRating != nil }
	return u.resolveSource(reqCtx, fetchCtx, key, hasID, hasValue, stale, canon, refresh, reload, valuePresent)
}

// --- shared state machine --------------------------------------------------

// resolveSource is the per-source SWR decision, generic over the source's refresh /
// reload / value-present closures. It NEVER returns an error (all fetch failures →
// pending/revalidate); the returned canon is the (possibly reloaded) row to read the
// value from.
func (u *SeriesRatingsUseCase) resolveSource(
	reqCtx, fetchCtx context.Context,
	key string,
	hasID, hasValue, stale bool,
	canon series.Canon,
	refresh func(context.Context) error,
	reload func(context.Context) (series.Canon, bool),
	valuePresent func(series.Canon) bool,
) (string, series.Canon) {
	switch {
	case !hasID:
		return dto.RatingStatusUnavailable, canon

	case hasValue && !stale:
		return dto.RatingStatusFresh, canon

	case hasValue && stale:
		// Return OLD value NOW; refresh in background (single-flight). Do NOT wait.
		u.kickBackground(reqCtx, key, refresh)
		return dto.RatingStatusRevalidating, canon

	case !hasValue && !stale:
		// Empty but freshly synced = genuine N/A (e.g. OMDb returned no rating). Do
		// not re-fetch; nothing to show.
		return dto.RatingStatusUnavailable, canon

	default: // !hasValue && stale (includes never-synced) — blocking fetch ≤ deadline.
		return u.blockingFetch(reqCtx, fetchCtx, key, canon, refresh, reload, valuePresent)
	}
}

// blockingFetch runs the refresh under single-flight and waits up to the shared
// fetch deadline. If a value lands in time → fresh (reloaded canon). If the deadline
// fires first → pending; the flight KEEPS RUNNING under its own detached context so
// the value persists for the FE's next poll. A single-flight already in progress
// (e.g. from a prior background kick) is joined, not duplicated.
func (u *SeriesRatingsUseCase) blockingFetch(
	reqCtx, fetchCtx context.Context,
	key string,
	canon series.Canon,
	refresh func(context.Context) error,
	reload func(context.Context) (series.Canon, bool),
	valuePresent func(series.Canon) bool,
) (string, series.Canon) {
	ch := u.sf.DoChan(key, func() (any, error) {
		// Detached context: outlives the request + the deadline so the refresh
		// completes even after we return pending. Precedent: admin/rest/media_pending.go.
		bgCtx, bgCancel := context.WithTimeout(context.WithoutCancel(reqCtx), u.d.BackgroundTimeout)
		defer bgCancel()
		return nil, refresh(bgCtx)
	})

	select {
	case res := <-ch:
		if res.Err != nil {
			u.d.Logger.WarnContext(reqCtx, "ratings.blocking_fetch_error",
				slog.String("key", key), slog.String("error", res.Err.Error()))
			return dto.RatingStatusPending, canon
		}
		// Re-read the canon to observe the owner-write. A blocking read on the
		// request ctx is fine here (fast, local DB).
		fresh, ok := reload(reqCtx)
		if ok && valuePresent(fresh) {
			return dto.RatingStatusFresh, fresh
		}
		// Fetched but still no value (budget-exhausted / terminal / genuine N/A).
		return dto.RatingStatusPending, canon
	case <-fetchCtx.Done():
		// Viewer deadline — flight continues in background (single-flight owns it).
		return dto.RatingStatusPending, canon
	}
}

// kickBackground launches a single-flight refresh that outlives the request. Repeated
// opens while a refresh is in flight are deduplicated by the group.
func (u *SeriesRatingsUseCase) kickBackground(reqCtx context.Context, key string, refresh func(context.Context) error) {
	go func() {
		_, _, _ = u.sf.Do(key, func() (any, error) {
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), u.d.BackgroundTimeout)
			defer cancel()
			if err := refresh(bgCtx); err != nil {
				u.d.Logger.WarnContext(bgCtx, "ratings.background_refresh_error",
					slog.String("key", key), slog.String("error", err.Error()))
			}
			return nil, nil
		})
	}()
}
