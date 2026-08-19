package adapters

import (
	"context"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ---- movie recs refresher holder -----------------------------------------

// movieRecsRefresher is the narrow movie rec-title refresh seam.
// *appenrich.MovieWorker satisfies it (RefreshRecommendations(ctx, movieID, lang)).
type movieRecsRefresher interface {
	RefreshRecommendations(ctx context.Context, movieID domain.MovieID, lang string) error
}

// MovieRecsRefresherHolder late-binds the MovieWorker into the movie recsPort's
// Refresh. Constructed in BuildMediaDetail (worker absent); server.go calls Set once
// the MovieWorker exists (movie late-bind zone). Until Set, Refresh returns errUnbound
// → engine Degraded → async fallback nudge (self-heals next open). Mirror of
// MovieCastRefresherHolder.
type MovieRecsRefresherHolder struct {
	mu    sync.RWMutex
	inner movieRecsRefresher
}

// NewMovieRecsRefresherHolder returns an unbound holder.
func NewMovieRecsRefresherHolder() *MovieRecsRefresherHolder { return &MovieRecsRefresherHolder{} }

// Set late-binds the worker. Idempotent; safe with concurrent RefreshRecommendations.
func (h *MovieRecsRefresherHolder) Set(w movieRecsRefresher) {
	h.mu.Lock()
	h.inner = w
	h.mu.Unlock()
}

// RefreshRecommendations drives the bound worker; errUnbound until Set.
func (h *MovieRecsRefresherHolder) RefreshRecommendations(ctx context.Context, movieID domain.MovieID, lang string) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return errUnbound
	}
	return inner.RefreshRecommendations(ctx, movieID, lang)
}

// ---- series recs refresher holder (dormant at runtime) -------------------

// seriesRecsRefresher is the narrow series rec-title refresh seam.
// *appenrich.SeriesWorker satisfies it (RefreshRecommendations drops force=false = on-view).
type seriesRecsRefresher interface {
	RefreshRecommendations(ctx context.Context, seriesID domain.SeriesID, lang string, force bool) error
}

// SeriesRecsRefresherHolder late-binds SeriesWorker into the series recsPort's Refresh.
// Registered but DORMANT: nothing drives the engine with a series MediaID at runtime
// (F-04/F-05 — series live recs stay on EnsureFreshScope). Bound anyway for parity.
type SeriesRecsRefresherHolder struct {
	mu    sync.RWMutex
	inner seriesRecsRefresher
}

// NewSeriesRecsRefresherHolder returns an unbound holder.
func NewSeriesRecsRefresherHolder() *SeriesRecsRefresherHolder { return &SeriesRecsRefresherHolder{} }

// Set late-binds the SeriesWorker.
func (h *SeriesRecsRefresherHolder) Set(w seriesRecsRefresher) {
	h.mu.Lock()
	h.inner = w
	h.mu.Unlock()
}

// RefreshRecommendations drives the bound worker with force=false (on-view, idempotent);
// errUnbound until Set.
func (h *SeriesRecsRefresherHolder) RefreshRecommendations(ctx context.Context, seriesID domain.SeriesID, lang string) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return errUnbound
	}
	return inner.RefreshRecommendations(ctx, seriesID, lang, false)
}

// ---- movie recs port -----------------------------------------------------

// movieRecsCoverageReader reads movie localized rec-title coverage.
// *enrichpersistence.MovieI18nReadRepository.MovieRecsCoverage satisfies it.
type movieRecsCoverageReader interface {
	MovieRecsCoverage(ctx context.Context, movieID domain.MovieID, lang string) (covered, total int, err error)
}

// movieRecsClockReader reads a movie canon for its enrichment_recs_synced_at.
// *enrichpersistence.MovieRepository.Get satisfies it.
type movieRecsClockReader interface {
	Get(ctx context.Context, id domain.MovieID) (movie.Canon, error)
}

// movieRecsPort adapts the movie readers + refresher to the type-agnostic RecsPort.
type movieRecsPort struct {
	cov     movieRecsCoverageReader
	canon   movieRecsClockReader
	refresh *MovieRecsRefresherHolder
}

// NewMovieRecsPort constructs the movie RecsPort.
func NewMovieRecsPort(cov movieRecsCoverageReader, canon movieRecsClockReader, refresh *MovieRecsRefresherHolder) RecsPort {
	return movieRecsPort{cov: cov, canon: canon, refresh: refresh}
}

func (m movieRecsPort) Coverage(ctx context.Context, id int64, lang string) (int, int, error) {
	return m.cov.MovieRecsCoverage(ctx, domain.MovieID(id), lang)
}

func (m movieRecsPort) RecsSyncedAt(ctx context.Context, id int64) (*time.Time, error) {
	c, err := m.canon.Get(ctx, domain.MovieID(id))
	if err != nil {
		return nil, err
	}
	return c.EnrichmentRecsSyncedAt, nil
}

func (m movieRecsPort) Refresh(ctx context.Context, id int64, lang string) error {
	return m.refresh.RefreshRecommendations(ctx, domain.MovieID(id), lang)
}

// ---- series recs port (dormant at runtime) -------------------------------

// seriesRecsCoverageReader reads series localized rec-title coverage.
// *enrichpersistence.SeriesTextsRepository.RecommendationsCoverage satisfies it.
type seriesRecsCoverageReader interface {
	RecommendationsCoverage(ctx context.Context, seriesID domain.SeriesID, lang string) (covered, total int, err error)
}

// seriesRecsClockReader reads a series canon for its enrichment_recs_synced_at.
// *enrichpersistence.SeriesRepository.Get satisfies it.
type seriesRecsClockReader interface {
	Get(ctx context.Context, id domain.SeriesID) (series.Canon, error)
}

// seriesRecsPort adapts the series readers + refresher to the RecsPort. Registered
// but dormant (nothing drives the engine with a series MediaID at runtime).
type seriesRecsPort struct {
	cov     seriesRecsCoverageReader
	canon   seriesRecsClockReader
	refresh *SeriesRecsRefresherHolder
}

// NewSeriesRecsPort constructs the series RecsPort.
func NewSeriesRecsPort(cov seriesRecsCoverageReader, canon seriesRecsClockReader, refresh *SeriesRecsRefresherHolder) RecsPort {
	return seriesRecsPort{cov: cov, canon: canon, refresh: refresh}
}

func (s seriesRecsPort) Coverage(ctx context.Context, id int64, lang string) (int, int, error) {
	return s.cov.RecommendationsCoverage(ctx, domain.SeriesID(id), lang)
}

func (s seriesRecsPort) RecsSyncedAt(ctx context.Context, id int64) (*time.Time, error) {
	c, err := s.canon.Get(ctx, domain.SeriesID(id))
	if err != nil {
		return nil, err
	}
	return c.EnrichmentRecsSyncedAt, nil
}

func (s seriesRecsPort) Refresh(ctx context.Context, id int64, lang string) error {
	return s.refresh.RefreshRecommendations(ctx, domain.SeriesID(id), lang)
}
