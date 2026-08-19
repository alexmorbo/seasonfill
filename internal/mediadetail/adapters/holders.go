// Package adapters holds the ADR-0022 composition seam: per-(MediaType,Section)
// SectionPlugin implementations that delegate to the vertical enrichment code,
// plus the late-bind holders that inject workers/engine after boot. This layer
// MAY import the engine and the verticals; the engine core imports neither.
package adapters

import (
	"context"
	"errors"
	"sync"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdengapp "github.com/alexmorbo/seasonfill/internal/mediadetail/app"
	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	mvapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// errUnbound is returned by a Refresh holder that has not yet been late-bound to
// its worker. The engine Freshener maps a Refresh error to Degraded → async
// fallback, so a boot-race open self-heals on the next tick (mirror of the
// MovieFreshener inner==nil branch).
var errUnbound = errors.New("mediadetail/adapters: refresher not late-bound")

// ---- movie force-refresher holder ----------------------------------------

// movieForced is the narrow refresh seam (a subset of moviedetail/app.MovieForceRefresher).
// *appenrich.MovieWorker satisfies it.
type movieForced interface {
	HandleForced(ctx context.Context, movieID int64) error
}

// MovieForceRefresherHolder late-binds the movie worker into movieTextPlugin.Refresh.
// Constructed in BuildMediaDetail (worker absent); server.go calls Set once the
// MovieWorker exists. Until Set, HandleForced returns errUnbound → engine Degraded.
type MovieForceRefresherHolder struct {
	mu    sync.RWMutex
	inner movieForced
}

// NewMovieForceRefresherHolder returns an unbound holder.
func NewMovieForceRefresherHolder() *MovieForceRefresherHolder { return &MovieForceRefresherHolder{} }

// Set late-binds the worker. Idempotent; safe with concurrent HandleForced.
func (h *MovieForceRefresherHolder) Set(w movieForced) {
	h.mu.Lock()
	h.inner = w
	h.mu.Unlock()
}

// HandleForced drives the bound worker; errUnbound until Set.
func (h *MovieForceRefresherHolder) HandleForced(ctx context.Context, movieID int64) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return errUnbound
	}
	return inner.HandleForced(ctx, movieID)
}

// ---- series all-langs refresher holder -----------------------------------

// seriesForced is the narrow series refresh seam. *appenrich.SeriesWorker
// satisfies it (RefreshSeriesAllLangs drops the lang arg = all-langs write).
type seriesForced interface {
	RefreshSeriesAllLangs(ctx context.Context, seriesID domain.SeriesID, force bool) error
}

// SeriesAllLangsRefresherHolder late-binds SeriesWorker into seriesTextPlugin.Refresh.
type SeriesAllLangsRefresherHolder struct {
	mu    sync.RWMutex
	inner seriesForced
}

// NewSeriesAllLangsRefresherHolder returns an unbound holder.
func NewSeriesAllLangsRefresherHolder() *SeriesAllLangsRefresherHolder {
	return &SeriesAllLangsRefresherHolder{}
}

// Set late-binds the SeriesWorker.
func (h *SeriesAllLangsRefresherHolder) Set(w seriesForced) {
	h.mu.Lock()
	h.inner = w
	h.mu.Unlock()
}

// RefreshSeriesAllLangs drives the bound worker with force=false (on-view, idempotent
// all-langs write); errUnbound until Set.
func (h *SeriesAllLangsRefresherHolder) RefreshSeriesAllLangs(ctx context.Context, seriesID domain.SeriesID) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return errUnbound
	}
	return inner.RefreshSeriesAllLangs(ctx, seriesID, false)
}

// ---- series overview-staleness holder (dormant at runtime) ---------------

// seriesOverviewStale is the narrow boolean overview-staleness seam. A runtime
// adapter over the series Probe would satisfy it, but the Probe is not reachable
// without editing seriesdetail wiring (forbidden), so at runtime this holder stays
// UNBOUND → {Stale:false} (safe; the series plugin is dormant). Unit tests inject a fake.
type seriesOverviewStale interface {
	OverviewStale(ctx context.Context, seriesID domain.SeriesID, lang string) (stale bool, reason string, err error)
}

// SeriesOverviewStalenessHolder late-binds (or leaves unbound) the series overview probe.
type SeriesOverviewStalenessHolder struct {
	mu    sync.RWMutex
	inner seriesOverviewStale
}

// NewSeriesOverviewStalenessHolder returns an unbound holder.
func NewSeriesOverviewStalenessHolder() *SeriesOverviewStalenessHolder {
	return &SeriesOverviewStalenessHolder{}
}

// Set late-binds a real overview probe adapter (optional).
func (h *SeriesOverviewStalenessHolder) Set(p seriesOverviewStale) {
	h.mu.Lock()
	h.inner = p
	h.mu.Unlock()
}

// OverviewStale reports the boolean overview verdict; unbound → (false,"unbound",nil).
func (h *SeriesOverviewStalenessHolder) OverviewStale(ctx context.Context, seriesID domain.SeriesID, lang string) (bool, string, error) {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return false, "unbound", nil
	}
	return inner.OverviewStale(ctx, seriesID, lang)
}

// ---- engine → moviedetail freshenerPort adapter (the REPLACE seam) --------

// MovieEngineFreshener satisfies moviedetail/app's (unexported) freshenerPort:
// EnsureFresh(ctx, movie.Canon, lang) mvapp.FreshenResult. It builds the engine
// MediaID from the already-loaded canon, drives the universal engine Freshener
// (which iterates the registered movie text plugin), and maps the engine's
// FreshenResult back to the movie one. This is the object wired via
// uc.WithFreshener, REPLACING the raw *MovieFreshener as the sole driver.
//
// Late-bind: the engine *Freshener is built after the movie uc, so the engine is
// injected via Set (mirror of the FreshenerHolder pattern). Until Set, EnsureFresh
// returns Degraded so the usecase's async fallback still nudges the row.
type MovieEngineFreshener struct {
	mu     sync.RWMutex
	engine *mdengapp.Freshener
}

// NewMovieEngineFreshener returns an unbound adapter.
func NewMovieEngineFreshener() *MovieEngineFreshener { return &MovieEngineFreshener{} }

// Set late-binds the engine Freshener. Idempotent.
func (a *MovieEngineFreshener) Set(engine *mdengapp.Freshener) {
	a.mu.Lock()
	a.engine = engine
	a.mu.Unlock()
}

// EnsureFresh drives the engine for the movie canon+lang and maps the result.
// canon.ID<=0 → Fresh (nothing to do). engine unbound → Degraded (async fallback).
func (a *MovieEngineFreshener) EnsureFresh(ctx context.Context, canon movie.Canon, lang string) mvapp.FreshenResult {
	if canon.ID <= 0 {
		return mvapp.FreshenResult{Fresh: true}
	}
	a.mu.RLock()
	engine := a.engine
	a.mu.RUnlock()
	if engine == nil {
		return mvapp.FreshenResult{Degraded: true}
	}
	var tmdbID domain.TMDBID
	if canon.TMDBID != nil {
		tmdbID = *canon.TMDBID
	}
	id, err := mdengdomain.NewMediaID(mdengdomain.MediaTypeMovie, int64(canon.ID), tmdbID)
	if err != nil {
		// A valid movie canon always yields a valid MediaID; on the impossible
		// error, do nothing (Fresh) rather than force a refresh.
		return mvapp.FreshenResult{Fresh: true}
	}
	res := engine.EnsureFresh(ctx, id, lang)
	return mvapp.FreshenResult{Refreshed: res.Refreshed, Fresh: res.Fresh, Degraded: res.Degraded}
}
