// prewarm.go implements the Discovery A2 pre-warm: at the end of every
// successful Worker.refresh() (kind, param, lang) cycle, iterate the
// items × active-langs matrix and call SeriesTextPreWarmer.PreWarm(...)
// per pair. Adds a bounded — probe-gated — window of TMDB fetches so
// the main /series/{id}?lang=X composer endpoint short-circuits on the
// first user click instead of blowing its Sync-mode 5s budget on a
// cold-lang miss.
//
// See internal/discovery/documentation/refactor-first/stories/568-a2-discovery-prewarm.md
// (local-only) for the full design rationale.
package app

import (
	"context"
	"errors"
	"log/slog"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/observability"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// PreWarm outcome labels — closed set exposed via
// observability.IncDiscoveryPrewarm. The worker is the sole writer.
//
// Cardinality note: at the discovery→enrichment port boundary
// (SeriesTextPreWarmer.PreWarm) we can only distinguish nil vs error vs
// ctx-cancelled. The finer-grained enrichment path (probe-fresh,
// no_tmdb_id_skip) is logged at DEBUG under domain="enrichment" from
// RefreshSeriesText itself — grep those to correlate. The
// discovery-side counter is intentionally coarse to keep the label
// budget small.
const (
	prewarmOutcomeWarmed       = "warmed"
	prewarmOutcomeSkipNoSeries = "skip_no_series"
	prewarmOutcomeError        = "error"
	prewarmOutcomeCancelled    = "cancelled"
	prewarmSummaryOp           = "prewarm_series_text_summary"
	prewarmSingleOp            = "prewarm_series_text"
	prewarmDomain              = "discovery"
)

// preWarmSeriesTexts fans out one PreWarm call per (item.SeriesID, lang)
// pair. Runs on the Worker.refresh() goroutine (single-threaded per Tick
// per PRD §5.1.1). Respects the shared rate limiter — every PreWarm call
// is preceded by w.limiter.Wait(ctx) so the combined refresh() + prewarm
// rate stays inside the B-39 5rps budget.
//
// Behavior:
//   - Nil-receiver-friendly: Worker.preWarmer == nil (config toggle OFF,
//     or unwired) → no-op early return.
//   - Empty items / langs → no-op early return.
//   - ctx cancellation mid-fan-out breaks the loop; an INFO
//     "prewarm_series_text_summary" line is emitted with the partial
//     counts. Outcome counters are still bumped for the pairs that were
//     dispatched.
//   - Per-call errors are absorbed (logged at DEBUG) so a single TMDB
//     failure does not poison the remainder of the fan-out.
//
// Called by Worker.refresh() after the successful ReplaceList branch —
// keep the arg list minimal so the call site stays a single line.
func (w *Worker) preWarmSeriesTexts(
	ctx context.Context,
	kind disco.Kind,
	param, listLang string,
	items []disco.Item,
	activeLangs []string,
) {
	if w.preWarmer == nil {
		return
	}
	if len(items) == 0 || len(activeLangs) == 0 {
		return
	}

	start := w.clock.Now()
	var (
		warmed        int
		skipNoSeries  int
		errored       int
		cancelledPair int
	)

	// Sequential fan-out: one goroutine, gated by the shared limiter.
	// The outer loop iterates active langs first so a partial ctx
	// cancellation still evenly covers series across langs (better UX
	// under load — every lang gets some fresh rows).
	for _, lang := range activeLangs {
		if ctx.Err() != nil {
			cancelledPair = countRemainingPairs(items, activeLangs, lang, len(items))
			break
		}
		for i, it := range items {
			if err := ctx.Err(); err != nil {
				cancelledPair += countRemainingPairs(items, activeLangs, lang, i)
				w.log.InfoContext(ctx, "discovery.prewarm.cancelled",
					slog.String("domain", prewarmDomain),
					slog.String("op", prewarmSingleOp),
					slog.String("kind", string(kind)),
					slog.String("language", lang),
					slog.String("error", err.Error()))
				break
			}
			if it.SeriesID <= 0 {
				// Defensive — Worker.materialiseItem returns SeriesID>0,
				// but the port contract is unowned data. Treat zero as
				// "no id available to pre-warm against".
				skipNoSeries++
				observability.IncDiscoveryPrewarm(prewarmOutcomeSkipNoSeries)
				continue
			}

			if err := w.limiter.Wait(ctx); err != nil {
				// Only ctx.Err surfaces here — treat as cancellation.
				cancelledPair += countRemainingPairs(items, activeLangs, lang, i)
				w.log.InfoContext(ctx, "discovery.prewarm.cancelled",
					slog.String("domain", prewarmDomain),
					slog.String("op", prewarmSingleOp),
					slog.String("kind", string(kind)),
					slog.String("language", lang),
					slog.String("error", err.Error()))
				break
			}

			outcome := w.preWarmOne(ctx, kind, lang, it.SeriesID)
			observability.IncDiscoveryPrewarm(outcome)
			switch outcome {
			case prewarmOutcomeWarmed:
				warmed++
			case prewarmOutcomeError:
				errored++
			case prewarmOutcomeCancelled:
				// Break outer loop next iteration via ctx.Err() check —
				// no double-increment since IncDiscoveryPrewarm above
				// already bumped the cancelled counter.
				cancelledPair++
			}
		}
	}

	dur := w.clock.Now().Sub(start)
	observability.ObserveDiscoveryPrewarmDuration(dur)

	w.log.InfoContext(ctx, prewarmSummaryOp,
		slog.String("domain", prewarmDomain),
		slog.String("op", prewarmSummaryOp),
		slog.String("kind", string(kind)),
		slog.String("param", param),
		slog.String("list_language", listLang),
		slog.Int("list_size", len(items)),
		slog.Int("langs", len(activeLangs)),
		slog.Int("warmed", warmed),
		slog.Int("skip_no_series", skipNoSeries),
		slog.Int("error", errored),
		slog.Int("cancelled", cancelledPair),
		slog.Int64("duration_ms", dur.Milliseconds()))
}

// preWarmOne dispatches ONE (seriesID, lang) PreWarm call and returns
// the outcome label. Per-item DEBUG log line so operators can grep the
// path without INFO spam.
func (w *Worker) preWarmOne(
	ctx context.Context,
	kind disco.Kind,
	lang string,
	seriesID shareddomain.SeriesID,
) string {
	start := w.clock.Now()
	err := w.preWarmer.PreWarm(ctx, seriesID, lang)
	dur := w.clock.Now().Sub(start)

	if err == nil {
		w.log.DebugContext(ctx, prewarmSingleOp,
			slog.String("domain", prewarmDomain),
			slog.String("op", prewarmSingleOp),
			slog.String("kind", string(kind)),
			slog.String("language", lang),
			slog.Int64("series_id", int64(seriesID)),
			slog.String("outcome", prewarmOutcomeWarmed),
			slog.Int64("duration_ms", dur.Milliseconds()))
		return prewarmOutcomeWarmed
	}

	// Classify. ctx cancellation surfaces as context.Canceled /
	// DeadlineExceeded — treat as cancelled. Everything else = error.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		w.log.DebugContext(ctx, prewarmSingleOp,
			slog.String("domain", prewarmDomain),
			slog.String("op", prewarmSingleOp),
			slog.String("kind", string(kind)),
			slog.String("language", lang),
			slog.Int64("series_id", int64(seriesID)),
			slog.String("outcome", prewarmOutcomeCancelled),
			slog.String("error", err.Error()))
		return prewarmOutcomeCancelled
	}

	w.log.DebugContext(ctx, prewarmSingleOp,
		slog.String("domain", prewarmDomain),
		slog.String("op", prewarmSingleOp),
		slog.String("kind", string(kind)),
		slog.String("language", lang),
		slog.Int64("series_id", int64(seriesID)),
		slog.String("outcome", prewarmOutcomeError),
		slog.String("error", err.Error()),
		slog.Int64("duration_ms", dur.Milliseconds()))
	return prewarmOutcomeError
}

// countRemainingPairs computes the count of (item × lang) pairs the
// worker would have dispatched had ctx not been cancelled. `curLangIdx`
// is the index inside activeLangs where cancellation was observed;
// `curItemIdx` is the index inside items at cancellation. Used to
// account cancelled outcomes accurately in the summary log line.
func countRemainingPairs(items []disco.Item, activeLangs []string, cancelLang string, curItemIdx int) int {
	if len(items) == 0 || len(activeLangs) == 0 {
		return 0
	}
	// Remaining pairs = remaining items in current lang + all items in
	// every subsequent lang.
	pairsRemainingInCurrentLang := max(0, len(items)-curItemIdx)
	langsAfterCurrent := 0
	seenCurrent := false
	for _, l := range activeLangs {
		if seenCurrent {
			langsAfterCurrent++
			continue
		}
		if l == cancelLang {
			seenCurrent = true
		}
	}
	return pairsRemainingInCurrentLang + (langsAfterCurrent * len(items))
}
