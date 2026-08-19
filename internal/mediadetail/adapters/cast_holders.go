package adapters

import (
	"context"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ---- movie cast refresher holder -----------------------------------------

// movieCastRefresher is the narrow movie cast-name refresh seam.
// *appenrich.MovieWorker satisfies it (RefreshCast(ctx, movieID, lang)).
type movieCastRefresher interface {
	RefreshCast(ctx context.Context, movieID domain.MovieID, lang string) error
}

// MovieCastRefresherHolder late-binds the MovieWorker into the movie castPort's
// Refresh. Constructed in BuildMediaDetail (worker absent); server.go calls Set once
// the MovieWorker exists (movie late-bind zone). Until Set, Refresh returns errUnbound
// → engine Degraded → async fallback nudge (self-heals next open). Mirror of
// MovieForceRefresherHolder.
type MovieCastRefresherHolder struct {
	mu    sync.RWMutex
	inner movieCastRefresher
}

// NewMovieCastRefresherHolder returns an unbound holder.
func NewMovieCastRefresherHolder() *MovieCastRefresherHolder { return &MovieCastRefresherHolder{} }

// Set late-binds the worker. Idempotent; safe with concurrent RefreshCast.
func (h *MovieCastRefresherHolder) Set(w movieCastRefresher) {
	h.mu.Lock()
	h.inner = w
	h.mu.Unlock()
}

// RefreshCast drives the bound worker; errUnbound until Set.
func (h *MovieCastRefresherHolder) RefreshCast(ctx context.Context, movieID domain.MovieID, lang string) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return errUnbound
	}
	return inner.RefreshCast(ctx, movieID, lang)
}

// ---- series cast refresher holder (dormant at runtime) -------------------

// seriesCastRefresher is the narrow series cast-name refresh seam.
// *appenrich.SeriesWorker satisfies it (RefreshCast drops force=false = on-view write).
type seriesCastRefresher interface {
	RefreshCast(ctx context.Context, seriesID domain.SeriesID, lang string, force bool) error
}

// SeriesCastRefresherHolder late-binds SeriesWorker into the series castPort's Refresh.
// Registered but DORMANT: nothing drives the engine with a series MediaID at runtime
// (F-05 — series live cast stays on EnsureFreshScope). Bound anyway for parity/future.
type SeriesCastRefresherHolder struct {
	mu    sync.RWMutex
	inner seriesCastRefresher
}

// NewSeriesCastRefresherHolder returns an unbound holder.
func NewSeriesCastRefresherHolder() *SeriesCastRefresherHolder { return &SeriesCastRefresherHolder{} }

// Set late-binds the SeriesWorker.
func (h *SeriesCastRefresherHolder) Set(w seriesCastRefresher) {
	h.mu.Lock()
	h.inner = w
	h.mu.Unlock()
}

// RefreshCast drives the bound worker with force=false (on-view, idempotent);
// errUnbound until Set.
func (h *SeriesCastRefresherHolder) RefreshCast(ctx context.Context, seriesID domain.SeriesID, lang string) error {
	h.mu.RLock()
	inner := h.inner
	h.mu.RUnlock()
	if inner == nil {
		return errUnbound
	}
	return inner.RefreshCast(ctx, seriesID, lang, false)
}

// ---- movie cast port -----------------------------------------------------

// movieCastCoverageReader reads movie localized cast-name coverage.
// *enrichpersistence.PeopleTextsRepository.MovieCastNameCoverage satisfies it.
type movieCastCoverageReader interface {
	MovieCastNameCoverage(ctx context.Context, movieID domain.MovieID, lang string) (covered, total int, err error)
}

// movieCastClockReader reads a movie canon for its enrichment_cast_synced_at.
// *enrichpersistence.MovieRepository.Get satisfies it.
type movieCastClockReader interface {
	Get(ctx context.Context, id domain.MovieID) (movie.Canon, error)
}

// movieCastPort adapts the movie readers + refresher to the type-agnostic CastPort.
type movieCastPort struct {
	cov     movieCastCoverageReader
	canon   movieCastClockReader
	refresh *MovieCastRefresherHolder
}

// NewMovieCastPort constructs the movie CastPort.
func NewMovieCastPort(cov movieCastCoverageReader, canon movieCastClockReader, refresh *MovieCastRefresherHolder) CastPort {
	return movieCastPort{cov: cov, canon: canon, refresh: refresh}
}

func (m movieCastPort) Coverage(ctx context.Context, id int64, lang string) (int, int, error) {
	return m.cov.MovieCastNameCoverage(ctx, domain.MovieID(id), lang)
}

func (m movieCastPort) CastSyncedAt(ctx context.Context, id int64) (*time.Time, error) {
	c, err := m.canon.Get(ctx, domain.MovieID(id))
	if err != nil {
		return nil, err
	}
	return c.EnrichmentCastSyncedAt, nil
}

func (m movieCastPort) Refresh(ctx context.Context, id int64, lang string) error {
	return m.refresh.RefreshCast(ctx, domain.MovieID(id), lang)
}

// ---- series cast port (dormant at runtime) -------------------------------

// seriesCastCoverageReader reads series localized cast-name coverage.
// *enrichpersistence.PeopleTextsRepository.CastNameCoverage satisfies it.
type seriesCastCoverageReader interface {
	CastNameCoverage(ctx context.Context, seriesID domain.SeriesID, lang string) (covered, total int, err error)
}

// seriesCastClockReader reads a series canon for its enrichment_cast_synced_at.
// *enrichpersistence.SeriesRepository.Get satisfies it.
type seriesCastClockReader interface {
	Get(ctx context.Context, id domain.SeriesID) (series.Canon, error)
}

// seriesCastPort adapts the series readers + refresher to the CastPort. Registered
// but dormant (nothing drives the engine with a series MediaID at runtime).
type seriesCastPort struct {
	cov     seriesCastCoverageReader
	canon   seriesCastClockReader
	refresh *SeriesCastRefresherHolder
}

// NewSeriesCastPort constructs the series CastPort.
func NewSeriesCastPort(cov seriesCastCoverageReader, canon seriesCastClockReader, refresh *SeriesCastRefresherHolder) CastPort {
	return seriesCastPort{cov: cov, canon: canon, refresh: refresh}
}

func (s seriesCastPort) Coverage(ctx context.Context, id int64, lang string) (int, int, error) {
	return s.cov.CastNameCoverage(ctx, domain.SeriesID(id), lang)
}

func (s seriesCastPort) CastSyncedAt(ctx context.Context, id int64) (*time.Time, error) {
	c, err := s.canon.Get(ctx, domain.SeriesID(id))
	if err != nil {
		return nil, err
	}
	return c.EnrichmentCastSyncedAt, nil
}

func (s seriesCastPort) Refresh(ctx context.Context, id int64, lang string) error {
	return s.refresh.RefreshCast(ctx, domain.SeriesID(id), lang)
}
