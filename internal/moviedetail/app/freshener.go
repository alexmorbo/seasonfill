package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// FreshenResult reports what the synchronous read-through freshener did for one
// movie-detail open. Exactly one of the three flags is the caller's headline:
//   - Fresh: the probe decided no refresh was needed (or the movie is not
//     eligible — tmdb-less / not-yet-wired guardrails collapse to Fresh only
//     when there is genuinely nothing to do; the boot race collapses to Degraded).
//   - Refreshed: HandleForced ran to completion within SyncTimeout and committed;
//     the caller MUST re-read the canon to observe the hydrated row.
//   - Degraded: the refresh timed out or errored (or the worker was not yet
//     late-bound); the caller falls back to the async mark-stale path and
//     surfaces a degraded[] marker. Mirror of seriesdetail.FreshenResult.
type FreshenResult struct {
	Refreshed bool
	Fresh     bool
	Degraded  bool
}

// MovieForceRefresher is the narrow enrichment seam the freshener drives. The
// production impl is *appenrich.MovieWorker (its HandleForced hydrates canon +
// i18n + cast + recs + media + keywords + collection in one COALESCE-safe pass).
// Declared locally so moviedetail/app never imports enrichment/app.
type MovieForceRefresher interface {
	HandleForced(ctx context.Context, movieID int64) error
}

// MovieI18nCoverageReader is the narrow read seam the freshener consults when the
// section-stamp probe says Fresh: it reports whether the requested non-base language
// has a localized-text gap (empty title/overview) that is due for a recheck. Impl:
// *enrichpersistence.MovieI18nReadRepository.HasLocalizedTextGap. nil-OK — an unwired
// reader collapses EnsureFresh to the pre-U-1b section-stamp-only behavior.
type MovieI18nCoverageReader interface {
	HasLocalizedTextGap(ctx context.Context, movieID domain.MovieID, lang string, recheckBefore time.Time) (bool, error)
}

// movieTitleGapRecheck bounds how often a still-empty localized row is re-hydrated on
// view. S-HEAL-FIX: keyed on the always-advancing movies.enrichment_text_synced_at
// attempt clock (see HasLocalizedTextGap), window 6h — kept in sync by contract with
// the background picker (movie_refresh_query.go) and the mediadetail plugin.
const movieTitleGapRecheck = 6 * time.Hour

// MovieFreshener is the movie analog of adapters.SeriesFreshenerHolder: a
// late-bound, singleflight-coalesced read-through freshener. Because movies have
// no per-section narrow refresh methods (HandleForced refreshes everything), the
// coalescing key is the whole movie per language — one in-flight HandleForced per
// (movieID, lang) no matter how many concurrent detail opens race.
//
// Lifecycle: constructed in wiring/moviedetail.go WITHOUT a worker (BuildMovieDetail
// runs before BuildMovieEnrichment); server.go's late-bind zone calls Set(worker)
// once the MovieWorker exists. Until Set, EnsureFresh returns Degraded so the
// usecase's async fallback still nudges the row (boot-race safe, mirrors the
// series inner==nil branch).
type MovieFreshener struct {
	syncTimeout time.Duration
	clock       func() time.Time
	log         *slog.Logger

	mu     sync.Mutex
	inner  MovieForceRefresher
	closed bool

	i18n MovieI18nCoverageReader

	sf singleflight.Group
}

// NewMovieFreshener constructs the holder. syncTimeout <= 0 → 5s (matches the
// series SyncTimeout, Story 567). clock nil → time.Now. log nil → default
// enrichment domain logger.
func NewMovieFreshener(syncTimeout time.Duration, clock func() time.Time, log *slog.Logger) *MovieFreshener {
	if syncTimeout <= 0 {
		syncTimeout = 5 * time.Second
	}
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	return &MovieFreshener{syncTimeout: syncTimeout, clock: clock, log: log}
}

// Set late-binds the enrichment worker. Idempotent; safe to call concurrently
// with EnsureFresh.
func (h *MovieFreshener) Set(w MovieForceRefresher) {
	h.mu.Lock()
	h.inner = w
	h.mu.Unlock()
}

// WithI18nCoverage wires the U-1b localized-text gap reader. When set, EnsureFresh
// escalates a section-Fresh movie to a synchronous HandleForced if the requested
// non-base language has an empty title/overview row due for a recheck. Returns the
// receiver for chaining in the wiring. nil-OK.
func (h *MovieFreshener) WithI18nCoverage(r MovieI18nCoverageReader) *MovieFreshener {
	h.i18n = r
	return h
}

// Close marks the holder shut down; subsequent EnsureFresh calls short-circuit to
// Fresh (cheap no-op during shutdown — nothing to hydrate on a draining server).
func (h *MovieFreshener) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
}

// EnsureFresh runs the pure MovieProbe over the already-loaded canon and, if any
// section is stale, drives MovieWorker.HandleForced synchronously under a
// SyncTimeout budget (singleflight-coalesced per movieID+lang). Contract:
//   - closed / canon.ID <= 0 / canon.TMDBID == nil  → Fresh (nothing to do).
//   - worker not yet late-bound (boot race)          → Degraded (async fallback).
//   - probe says fresh                               → Fresh (no HandleForced).
//   - stale + HandleForced OK within budget          → Refreshed (caller re-reads).
//   - stale + timeout / error                        → Degraded (async fallback).
//
// The HandleForced call runs on a DETACHED ctx (context.Background) with the
// SyncTimeout deadline + tmdb.WithInteractive, so (a) a coalesced caller's
// cancellation cannot abort the shared leader, and (b) the on-view TMDB calls
// draw from the full rps bucket. On timeout EnsureFresh returns immediately
// (Degraded); the leader goroutine runs to completion under singleflight and
// commits whatever it can — self-healing for the next open.
func (h *MovieFreshener) EnsureFresh(ctx context.Context, canon movie.Canon, lang string) FreshenResult {
	if canon.ID <= 0 || canon.TMDBID == nil {
		return FreshenResult{Fresh: true}
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return FreshenResult{Fresh: true}
	}
	inner := h.inner
	h.mu.Unlock()

	if inner == nil {
		// Boot race: MovieWorker not yet late-bound. Let the usecase's async
		// fallback nudge the row. Mirror of the series inner==nil branch.
		return FreshenResult{Degraded: true}
	}

	if !h.isStale(ctx, canon, lang) {
		return FreshenResult{Fresh: true}
	}

	movieID := int64(canon.ID)
	key := fmt.Sprintf("movie-%d:%s", movieID, lang)

	// Detached ctx: the singleflight leader must survive one caller's cancel.
	refreshCtx, cancel := context.WithTimeout(context.Background(), h.syncTimeout)
	defer cancel()
	refreshCtx = tmdb.WithInteractive(refreshCtx)

	start := h.clock()
	done := make(chan error, 1)
	go func() {
		_, err, _ := h.sf.Do(key, func() (any, error) {
			defer h.sf.Forget(key)
			return nil, inner.HandleForced(refreshCtx, movieID)
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			h.log.WarnContext(ctx, "moviedetail.freshener.refresh_error",
				slog.Int64("movie_id", movieID),
				slog.String("lang", lang),
				slog.String("error", err.Error()),
			)
			return FreshenResult{Degraded: true}
		}
		h.log.InfoContext(ctx, "moviedetail.freshener.refreshed",
			slog.Int64("movie_id", movieID),
			slog.String("lang", lang),
			slog.Int64("duration_ms", h.clock().Sub(start).Milliseconds()),
		)
		return FreshenResult{Refreshed: true}
	case <-refreshCtx.Done():
		// Sync budget exhausted. Leader keeps running under singleflight; the
		// usecase's async fallback covers the gap + surfaces degraded[].
		h.log.WarnContext(ctx, "moviedetail.freshener.timeout",
			slog.Int64("movie_id", movieID),
			slog.String("lang", lang),
			slog.Int64("budget_ms", h.syncTimeout.Milliseconds()),
		)
		return FreshenResult{Degraded: true}
	}
}

// isStale folds the pure section-stamp probe with the U-1b localized-text gap check.
// The section probe fires first (cheap, no IO). Only when it says Fresh AND a coverage
// reader is wired AND the requested lang is a non-base language do we spend one indexed
// EXISTS read to catch the "fresh stamps but empty ru title" hole (the ~10,806). A read
// error fails CLOSED (logged, treated as Fresh) so a flaky read never fires a 5s TMDB
// hydrate on the request path — the background picker is the durable backstop.
func (h *MovieFreshener) isStale(ctx context.Context, canon movie.Canon, lang string) bool {
	if AnyStale(MovieProbe(canon, h.clock())) {
		return true
	}
	if h.i18n == nil || lang == "" || lang == locale.Default() {
		return false
	}
	recheckBefore := h.clock().Add(-movieTitleGapRecheck)
	gap, err := h.i18n.HasLocalizedTextGap(ctx, canon.ID, lang, recheckBefore)
	if err != nil {
		h.log.WarnContext(ctx, "moviedetail.freshener.coverage_error",
			slog.Int64("movie_id", int64(canon.ID)),
			slog.String("lang", lang),
			slog.String("error", err.Error()),
		)
		return false
	}
	return gap
}
