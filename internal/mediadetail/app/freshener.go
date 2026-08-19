package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// AsyncFallback is the hook the Freshener calls when a synchronous refresh
// cannot complete (timeout / error / unbound engine). It should enqueue an
// off-request hydration nudge (the dispatcher Hot lane). nil-safe: an unbound
// hook means no async nudge (the background sweeper is the durable backstop).
type AsyncFallback func(id domain.MediaID, lang string)

// Freshener is the generic, type-agnostic read-through freshener — the single
// engine analog of seriesdetail's EnsureFreshScope and moviedetail's
// MovieFreshener, unified. For a (MediaID, lang) it iterates the registered
// plugins for the media type, folds each plugin's Coverage + Staleness into a
// stale/not-stale decision, and — if anything is stale — drives the stale
// plugins' Refresh synchronously under a syncTimeout budget, coalesced per
// (MediaID, lang) via singleflight. One Freshener serves BOTH verticals.
//
// Late-binding: the async fallback is bound via SetAsyncFallback at the
// composition root's late-bind zone (the dispatcher does not exist at
// BuildMediaDetail time). The registry is passed at construction and mutated
// in-place by later stories' plugin registration (also at the late-bind zone),
// so the Freshener always sees the current plugin set.
//
// S1: the registry is EMPTY, so EnsureFresh always returns Fresh (proved by a
// test) — zero runtime behavior change.
type Freshener struct {
	registry    *SectionRegistry
	syncTimeout time.Duration
	clock       func() time.Time
	log         *slog.Logger

	mu       sync.Mutex
	fallback AsyncFallback
	closed   bool

	sf singleflight.Group
}

// NewFreshener constructs the engine freshener over registry. syncTimeout <= 0 →
// 5s (matches the series/movie sync budget). clock nil → time.Now. log nil →
// default domain logger. registry MUST be non-nil (the empty registry from
// NewSectionRegistry is fine).
func NewFreshener(registry *SectionRegistry, syncTimeout time.Duration, clock func() time.Time, log *slog.Logger) *Freshener {
	if syncTimeout <= 0 {
		syncTimeout = 5 * time.Second
	}
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	return &Freshener{registry: registry, syncTimeout: syncTimeout, clock: clock, log: log}
}

// SetAsyncFallback late-binds the async fallback hook. Idempotent; safe to call
// concurrently with EnsureFresh. Returns the receiver for wiring chains.
func (f *Freshener) SetAsyncFallback(fn AsyncFallback) *Freshener {
	f.mu.Lock()
	f.fallback = fn
	f.mu.Unlock()
	return f
}

// Close marks the freshener shut down; subsequent EnsureFresh calls short to
// Fresh (cheap no-op on a draining server).
func (f *Freshener) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// EnsureFresh runs the read-through freshen for id+lang. Contract:
//   - invalid id / closed                         → Fresh (nothing to do).
//   - registry unbound (nil)                       → Degraded (async fallback).
//   - no plugins registered for the type           → Fresh (nothing to do).
//   - no plugin stale                              → Fresh.
//   - stale + Refresh OK within budget             → Refreshed (caller re-reads).
//   - stale + timeout / error                      → Degraded (async fallback).
//
// The Refresh runs on a DETACHED ctx (context.Background + syncTimeout) under
// singleflight, so one coalesced caller's cancellation cannot abort the shared
// leader; on timeout EnsureFresh returns Degraded immediately while the leader
// runs to completion and commits what it can (self-heal for the next open).
func (f *Freshener) EnsureFresh(ctx context.Context, id domain.MediaID, lang string) domain.FreshenResult {
	if !id.Valid() {
		return domain.FreshenResult{Fresh: true}
	}

	f.mu.Lock()
	closed := f.closed
	fallback := f.fallback
	f.mu.Unlock()
	if closed {
		return domain.FreshenResult{Fresh: true}
	}
	if f.registry == nil {
		return domain.FreshenResult{Degraded: true}
	}

	plugins := f.registry.For(id.Type())
	if len(plugins) == 0 {
		return domain.FreshenResult{Fresh: true}
	}

	now := f.clock()
	stale := make([]SectionPlugin, 0, len(plugins))
	for _, p := range plugins {
		if f.assess(ctx, p, id, lang, now) {
			stale = append(stale, p)
		}
	}
	if len(stale) == 0 {
		return domain.FreshenResult{Fresh: true}
	}

	return f.refresh(ctx, id, lang, stale, fallback)
}

// assess folds a plugin's Coverage + Staleness into a single stale bool. Both
// checks fail CLOSED on IO error (contribute "not stale") so a flaky read never
// triggers a synchronous refresh. Coverage counts only when total>0 (the no-op
// contract); Staleness counts when its verdict Stale is true.
func (f *Freshener) assess(ctx context.Context, p SectionPlugin, id domain.MediaID, lang string, now time.Time) bool {
	covered, total, err := p.Coverage(ctx, id, lang)
	if err != nil {
		f.log.WarnContext(ctx, "mediadetail.freshener.coverage_error",
			slog.String("media", id.Key()),
			slog.String("section", p.Section().String()),
			slog.String("lang", lang),
			slog.String("error", err.Error()),
		)
	} else if total > 0 && covered < total {
		return true
	}

	v, err := p.Staleness(ctx, id, lang, now)
	if err != nil {
		f.log.WarnContext(ctx, "mediadetail.freshener.staleness_error",
			slog.String("media", id.Key()),
			slog.String("section", p.Section().String()),
			slog.String("lang", lang),
			slog.String("error", err.Error()),
		)
		return false
	}
	return v.Stale
}

// refresh drives the stale plugins' Refresh under a detached sync budget,
// coalesced per (MediaID, lang). On timeout/error it invokes the async fallback
// (if bound) and returns Degraded.
func (f *Freshener) refresh(ctx context.Context, id domain.MediaID, lang string, stale []SectionPlugin, fallback AsyncFallback) domain.FreshenResult {
	key := id.Key() + ":" + lang

	refreshCtx, cancel := context.WithTimeout(context.Background(), f.syncTimeout)
	defer cancel()

	start := f.clock()
	done := make(chan error, 1)
	go func() {
		_, err, _ := f.sf.Do(key, func() (any, error) {
			defer f.sf.Forget(key)
			return nil, f.refreshAll(refreshCtx, id, lang, stale)
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			f.log.WarnContext(ctx, "mediadetail.freshener.refresh_error",
				slog.String("media", id.Key()),
				slog.String("lang", lang),
				slog.String("error", err.Error()),
			)
			f.nudge(fallback, id, lang)
			return domain.FreshenResult{Degraded: true}
		}
		f.log.InfoContext(ctx, "mediadetail.freshener.refreshed",
			slog.String("media", id.Key()),
			slog.String("lang", lang),
			slog.Int("sections", len(stale)),
			slog.Int64("duration_ms", f.clock().Sub(start).Milliseconds()),
		)
		return domain.FreshenResult{Refreshed: true}
	case <-refreshCtx.Done():
		f.log.WarnContext(ctx, "mediadetail.freshener.timeout",
			slog.String("media", id.Key()),
			slog.String("lang", lang),
			slog.Int64("budget_ms", f.syncTimeout.Milliseconds()),
		)
		f.nudge(fallback, id, lang)
		return domain.FreshenResult{Degraded: true}
	}
}

// refreshAll runs each stale plugin's Refresh sequentially. The first error
// stops the pass and is returned (the leader commits whatever earlier plugins
// wrote; the next open re-assesses the remainder).
func (f *Freshener) refreshAll(ctx context.Context, id domain.MediaID, lang string, stale []SectionPlugin) error {
	for _, p := range stale {
		if err := p.Refresh(ctx, id, lang); err != nil {
			return err
		}
	}
	return nil
}

// nudge invokes the async fallback if bound. nil-safe.
func (f *Freshener) nudge(fallback AsyncFallback, id domain.MediaID, lang string) {
	if fallback != nil {
		fallback(id, lang)
	}
}
