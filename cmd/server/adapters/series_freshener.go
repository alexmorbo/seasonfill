package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/observability"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SeriesFreshenerConfig configures the freshener. Probe + AsyncEnricher
// are required; the rest fall back to safe defaults.
type SeriesFreshenerConfig struct {
	Probe         StalenessProbe
	AsyncEnricher seriesdetail.OnDemandEnricher
	SyncTimeout   time.Duration // default 3s
	Logger        *slog.Logger
}

// SeriesWorkerHandle is the narrow contract the freshener consumes from
// *appenrich.SeriesWorker. Local interface so tests inject a fake worker
// without standing up the full TMDB + persistence dependency graph.
//
// HandleForcedLang (Story 546) is the read-through entry point: ONE GetTV
// call + ONE tx commits series-level data (canon, series_texts[lang],
// season shells, cast, taxonomy, videos, ratings, external IDs,
// recommendations). Episodes/episode_texts/episode_credits land via the
// background dispatcher pass triggered by AsyncEnricher.EnqueueIfStale.
// Pre-546 the Freshener called HandleForced, which iterated every
// w.deps.Languages entry AND fetched every active season's episode list
// per language — on a 9-season series this consistently blew the 3s
// budget on ru-RU and rolled back the entire tx (no series_texts.ru-RU
// row written). HandleForced + Handle stay on the interface so the test
// fakes keep their existing routing assertions (Story 544 regression).
type SeriesWorkerHandle interface {
	Handle(ctx context.Context, seriesID domain.SeriesID) error
	HandleForced(ctx context.Context, seriesID domain.SeriesID) error
	HandleForcedLang(ctx context.Context, seriesID domain.SeriesID, lang string) error
}

// SeriesFreshenerHolder satisfies seriesdetail.SeriesFreshener. Wraps a
// late-bound *appenrich.SeriesWorker (set after wireEnrichment by
// cmd/server/server.go's LATE BIND ZONE — same pattern as
// OnDemandEnricherHolder + PersonEnqueuerHolder).
//
// EnsureFresh flow:
//  1. staleness probe — fresh → return Fresh=true.
//  2. singleflight per (seriesID, lang) — coalesces concurrent first
//     opens onto a SINGLE worker.Handle call.
//  3. inside SF: detached ctx with SyncTimeout. Worker.Handle. On nil
//     err → Refreshed=true. On timeout/error → log + EnqueueIfStale
//     async fallback → Degraded=true.
type SeriesFreshenerHolder struct {
	cfg SeriesFreshenerConfig

	mu     sync.Mutex
	inner  SeriesWorkerHandle
	closed bool

	sf singleflight.Group
}

var _ seriesdetail.SeriesFreshener = (*SeriesFreshenerHolder)(nil)

// NewSeriesFreshenerHolder constructs the holder. Probe + AsyncEnricher
// required.
func NewSeriesFreshenerHolder(cfg SeriesFreshenerConfig) (*SeriesFreshenerHolder, error) {
	if cfg.Probe == nil {
		return nil, errors.New("seriesfreshener: Probe required")
	}
	if cfg.AsyncEnricher == nil {
		return nil, errors.New("seriesfreshener: AsyncEnricher required")
	}
	if cfg.SyncTimeout <= 0 {
		cfg.SyncTimeout = 3 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	return &SeriesFreshenerHolder{cfg: cfg}, nil
}

// Set wires the inner worker. Idempotent. Accepts the narrow
// SeriesWorkerHandle interface so production passes *appenrich.SeriesWorker
// and tests pass a fake.
func (h *SeriesFreshenerHolder) Set(w SeriesWorkerHandle) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inner = w
}

// Close marks the holder shut down; subsequent EnsureFresh calls return
// Fresh=true (cheap no-op during shutdown).
func (h *SeriesFreshenerHolder) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
}

// EnsureFresh — see SeriesFreshener doc.
func (h *SeriesFreshenerHolder) EnsureFresh(ctx context.Context, seriesID domain.SeriesID, lang string) seriesdetail.FreshenResult {
	start := time.Now()
	if seriesID <= 0 {
		observability.IncSeriesdetailFreshen("skipped")
		return seriesdetail.FreshenResult{Fresh: true}
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		observability.IncSeriesdetailFreshen("skipped")
		return seriesdetail.FreshenResult{Fresh: true}
	}
	inner := h.inner
	h.mu.Unlock()

	stale, reason := h.cfg.Probe.IsStale(ctx, seriesID, lang)
	if !stale {
		observability.IncSeriesdetailFreshen("fresh")
		h.cfg.Logger.DebugContext(ctx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", "fresh"),
			slog.String("reason", reason),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
		return seriesdetail.FreshenResult{Fresh: true}
	}

	if inner == nil {
		// Boot race: enrichment dispatcher not yet wired. Fall back to
		// async (Story 528). Best-effort.
		h.cfg.AsyncEnricher.EnqueueIfStale(seriesID, catalogseries.HydrationStub)
		observability.IncSeriesdetailFreshen("async_only")
		h.cfg.Logger.InfoContext(ctx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", "async_only"),
			slog.String("reason", reason),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
		return seriesdetail.FreshenResult{Degraded: true}
	}

	key := fmt.Sprintf("%d:%s", int64(seriesID), lang)
	v, sferr, _ := h.sf.Do(key, func() (any, error) {
		defer h.sf.Forget(key)
		// Detached ctx — coalesced callers share one HandleForcedLang
		// invocation, and one caller's cancellation must not abort the
		// others. HandleForcedLang (Story 546) is the staged path: one
		// GetTV + one tx, no per-season fetches.
		freshenCtx, cancel := context.WithTimeout(context.Background(), h.cfg.SyncTimeout)
		defer cancel()
		if err := inner.HandleForcedLang(freshenCtx, seriesID, lang); err != nil {
			return err, err
		}
		// Stage 1+2 committed (series-level data + lang text). Story 546:
		// schedule the background pass that fills episodes/episode_texts
		// via the dispatcher path. Fired INSIDE the singleflight Do() so a
		// coalesced batch of N "first opens" produces ONE EnqueueIfStale
		// call, not N — the OnDemandEnricher's 30s throttle would dedup
		// in prod anyway, but keeping it tight here means tests + metrics
		// observe a clean 1:1 between staged commits and follow-up
		// enqueues. The worker's Handle freshness gate is intentionally
		// NOT stamped by HandleForcedLang, so the follow-up runs end-to-end.
		h.cfg.AsyncEnricher.EnqueueIfStale(seriesID, catalogseries.HydrationStub)
		return nil, nil
	})

	durMs := time.Since(start).Milliseconds()
	switch {
	case sferr == nil && v == nil:
		observability.IncSeriesdetailFreshen("refreshed")
		h.cfg.Logger.InfoContext(ctx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", "refreshed"),
			slog.String("reason", reason),
			slog.Int64("duration_ms", durMs),
			slog.String("followup", "enqueued"),
		)
		return seriesdetail.FreshenResult{Refreshed: true}
	default:
		label := "error"
		if errors.Is(sferr, context.DeadlineExceeded) {
			label = "timeout"
		}
		h.cfg.AsyncEnricher.EnqueueIfStale(seriesID, catalogseries.HydrationStub)
		observability.IncSeriesdetailFreshen(label)
		h.cfg.Logger.WarnContext(ctx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", label),
			slog.String("reason", reason),
			slog.Int64("duration_ms", durMs),
			slog.String("error", freshenErrString(sferr)),
		)
		return seriesdetail.FreshenResult{Degraded: true}
	}
}

func freshenErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
