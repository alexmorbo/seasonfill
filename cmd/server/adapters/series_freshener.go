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
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SeriesFreshenerConfig configures the freshener. Probe + AsyncEnricher
// are required; the rest fall back to safe defaults.
type SeriesFreshenerConfig struct {
	Probe         freshener.Probe
	AsyncEnricher seriesdetail.OnDemandEnricher
	SyncTimeout   time.Duration // default 5s (Story 567 — up from 3s)
	Logger        *slog.Logger
}

// SeriesWorkerHandle is the narrow contract the freshener consumes from
// *appenrich.SeriesWorker. Local interface so tests inject a fake worker
// without standing up the full TMDB + persistence dependency graph.
//
// A5 (Story 563) added the five narrow refresh methods. Handle /
// HandleForced / HandleForcedLang stay on the interface because they
// back the SectionSkeleton verdict AND the pre-A5 EnsureFresh shim.
type SeriesWorkerHandle interface {
	Handle(ctx context.Context, seriesID domain.SeriesID) error
	HandleForced(ctx context.Context, seriesID domain.SeriesID) error
	HandleForcedLang(ctx context.Context, seriesID domain.SeriesID, lang string) error

	// A5 narrow methods. All accept force bool (F-R2-5).
	RefreshSeriesText(ctx context.Context, seriesID domain.SeriesID, lang string, force bool) error
	RefreshCast(ctx context.Context, seriesID domain.SeriesID, lang string, force bool) error
	RefreshSeasonSlim(ctx context.Context, seriesID domain.SeriesID, seasonNumber int, lang string, force bool) error
	RefreshRecommendations(ctx context.Context, seriesID domain.SeriesID, lang string, force bool) error
	RefreshMediaAssets(ctx context.Context, seriesID domain.SeriesID, lang string, force bool) error

	// RefreshSeriesAllLangs — S-B: one GetTV → series_texts +
	// series_media_texts for ALL supported languages. The SectionOverview
	// dispatch routes here (superseding the per-lang RefreshSeriesText for the
	// freshener path). RefreshSeriesText stays for Phase-4 targeted per-iso
	// refresh.
	RefreshSeriesAllLangs(ctx context.Context, seriesID domain.SeriesID, force bool) error
}

// SeriesFreshenerHolder satisfies seriesdetail.SeriesFreshener. Wraps a
// late-bound *appenrich.SeriesWorker (set after wireEnrichment).
//
// EnsureFreshScope flow (A5, Story 563):
//  1. Validate seriesID + closed check → nil-work early return.
//  2. Validate lang via VO (invalid → zero LanguageTag).
//  3. Probe.IsStale → filter verdicts to caller-requested sections.
//  4. Dispatch per verdict via singleflight (key includes section).
//  5. Sync: WaitGroup + SyncTimeout; Async: detached goroutines + 180s.
//  6. Combine outcomes → FreshenResult + errors.Join.
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
		cfg.SyncTimeout = 5 * time.Second
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

// asyncFollowupTimeout — 180s constant used pre-A5 spawnAsyncFollowup.
// Async-mode dispatch budget. Detached ctx so caller cancellation does
// not abort background work. Story 548 bumped it 60→180s to survive TMDB
// rate-limit adaptive pauses across a 9-season × 2-lang fan-out.
const asyncFollowupTimeout = 180 * time.Second

// defaultShimSections — the canned Section list used by the EnsureFresh
// legacy shim. Matches the pre-A5 monolithic HandleForcedLang path plus
// A2/A3b/A4 narrow methods so shim callers get parity dispatch.
var defaultShimSections = []freshener.Section{
	freshener.SectionSkeleton,
	freshener.SectionOverview,
	freshener.SectionCast,
	freshener.SectionRecommendations,
	freshener.SectionMedia,
}

// EnsureFresh — legacy shim. Delegates to EnsureFreshScope with a canned
// section list matching pre-A5 monolithic behavior. Kept for test
// fixtures + tmdb_fallback callsites during incremental migration.
// Post-Phase-2 removal after all callsites move to EnsureFreshScope.
func (h *SeriesFreshenerHolder) EnsureFresh(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
) seriesdetail.FreshenResult {
	res, _ := h.EnsureFreshScope(ctx, seriesID, lang,
		defaultShimSections,
		nil,   // seasonNumbers
		false, // force
		seriesdetail.ModeSync,
	)
	return res
}

// EnsureFreshScope — see SeriesFreshener doc (Story 563 spec).
//
// Steps:
//  1. Validate seriesID + closed check → nil-work early return.
//  2. Validate lang via VO (invalid → zero LanguageTag, log at Debug).
//  3. Probe.IsStale(ctx, seriesID, langVO, mergeSeasonNumbers(sections)).
//     ctx.Err → Degraded=true. Other IO errors already fail-open inside Probe.
//  4. Filter Probe verdicts to caller-requested sections AND Stale=true.
//     force=true → all requested sections dispatched regardless of Probe.
//  5. Compute inner worker; nil (boot race) → fallback to
//     AsyncEnricher.EnqueueIfStale + Degraded=true.
//  6. Dispatch per verdict via h.sf.Do(key), with per-section goroutine
//     under Sync WaitGroup or detached goroutine under Async.
//  7. Sync: wait for goroutines OR SyncTimeout. Collect per-section errors.
//  8. Combine outcomes → FreshenResult{Refreshed/Degraded/Fresh} + errors.Join.
//     Bump observability metric with result label.
func (h *SeriesFreshenerHolder) EnsureFreshScope(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
	sections []freshener.Section,
	seasonNumbers []int,
	force bool,
	mode seriesdetail.EnsureFreshMode,
) (seriesdetail.FreshenResult, error) {
	start := time.Now()

	if seriesID <= 0 {
		observability.IncSeriesdetailFreshen("skipped")
		return seriesdetail.FreshenResult{Fresh: true}, nil
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		observability.IncSeriesdetailFreshen("skipped")
		return seriesdetail.FreshenResult{Fresh: true}, nil
	}
	inner := h.inner
	h.mu.Unlock()

	langVO, langErr := values.NewLanguageTag(lang)
	if langErr != nil {
		h.cfg.Logger.DebugContext(ctx, "freshen.invalid_lang",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("error", langErr.Error()),
		)
		langVO = values.LanguageTag{}
	}

	// Extract sparse season numbers if caller passed them explicitly OR
	// if any Section arg is a "season:N". Deduplicate to keep Probe's
	// slice tidy.
	probeSeasons := mergeSeasonNumbers(seasonNumbers, sections)
	verdicts, probeErr := h.cfg.Probe.IsStale(ctx, seriesID, langVO, probeSeasons)
	if probeErr != nil {
		observability.IncSeriesdetailFreshen("error")
		h.cfg.Logger.WarnContext(ctx, "freshen.probe_error",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("error", probeErr.Error()),
		)
		return seriesdetail.FreshenResult{Degraded: true}, probeErr
	}

	// If force=true, dispatch every requested section without consulting
	// Probe (still emit the log/metric for observability). If force=false,
	// intersect requested sections with Stale=true verdicts.
	dispatchPlan := planDispatch(sections, verdicts, force)
	if len(dispatchPlan) == 0 {
		observability.IncSeriesdetailFreshen("fresh")
		h.cfg.Logger.DebugContext(ctx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", "fresh"),
			slog.Int("sections_requested", len(sections)),
			slog.Int("sections_stale", 0),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
		return seriesdetail.FreshenResult{Fresh: true}, nil
	}

	if inner == nil {
		// Boot race: enrichment dispatcher not yet wired. Fall back to
		// Story 528 async path. Best-effort.
		h.cfg.AsyncEnricher.EnqueueIfStale(seriesID, catalogseries.HydrationStub)
		observability.IncSeriesdetailFreshen("async_only")
		h.cfg.Logger.InfoContext(ctx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", "async_only"),
			slog.Int("sections_stale", len(dispatchPlan)),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
		return seriesdetail.FreshenResult{Degraded: true}, nil
	}

	// Dispatch.
	switch mode {
	case seriesdetail.ModeAsync:
		return h.dispatchAsync(inner, seriesID, lang, dispatchPlan, force, start), nil
	default: // ModeSync
		return h.dispatchSync(ctx, inner, seriesID, lang, dispatchPlan, force, start)
	}
}

// dispatchSync fires all planned sections in parallel goroutines under
// a single ctx with SyncTimeout deadline. Waits for all goroutines to
// finish OR the ctx to timeout. Per-section errors collected + returned
// via errors.Join. Partial success: sections that finished before
// timeout have their tx committed; timed-out sections logged + counted.
func (h *SeriesFreshenerHolder) dispatchSync(
	parentCtx context.Context,
	inner SeriesWorkerHandle,
	seriesID domain.SeriesID,
	lang string,
	plan []dispatchItem,
	force bool,
	start time.Time,
) (seriesdetail.FreshenResult, error) {
	// Detached ctx with SyncTimeout — coalesced callers share one narrow
	// method invocation via singleflight, so one caller's cancellation
	// must NOT abort the others. SyncTimeout caps the entire scope; the
	// composer's degraded[] projection handles partial completions.
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.SyncTimeout)
	defer cancel()
	// W110-5 (F-03) — this is the on-view path: mark the detached ctx interactive
	// so the freshener's TMDB calls draw from the FULL rps bucket and keep
	// headroom under batch-enrichment saturation. The marker rides ctx through
	// runOne → invokeNarrow → SeriesWorker.Refresh* → TMDBClientHolder →
	// tmdb.Client.do → doDirect. Batch (enrichment dispatcher) and background
	// (dispatchAsync / carryOverAsync, SWR revalidate) paths stay UNMARKED.
	ctx = tmdb.WithInteractive(ctx)

	var wg sync.WaitGroup
	errCh := make(chan sectionError, len(plan))

	for _, item := range plan {
		wg.Go(func() {
			err := h.runOne(ctx, inner, seriesID, lang, item, force)
			if err != nil {
				errCh <- sectionError{section: item.section, err: err}
			}
		})
	}

	// Wait via a helper channel so we can respect parentCtx cancellation
	// (return early if caller aborts, though goroutines under detached
	// ctx keep running to completion — same pattern as pre-A5 SF closure).
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	var errs []sectionError
	select {
	case <-done:
		close(errCh)
		for e := range errCh {
			errs = append(errs, e)
		}
	case <-parentCtx.Done():
		// Caller aborted. Goroutines continue under detached ctx; log
		// this as a partial and let the composer proceed. This mirrors
		// pre-A5 SF behavior where a single caller's Cancel didn't abort
		// the singleflight leader.
		h.cfg.Logger.InfoContext(parentCtx, "freshen.parent_ctx_done",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.Int("in_flight", len(plan)),
			slog.String("error", parentCtx.Err().Error()),
		)
		// Best-effort belt-and-suspenders — enqueue async.
		h.cfg.AsyncEnricher.EnqueueIfStale(seriesID, catalogseries.HydrationStub)
		return seriesdetail.FreshenResult{Degraded: true}, parentCtx.Err()
	}

	dur := time.Since(start).Milliseconds()
	total := len(plan)
	failed := len(errs)

	switch {
	case failed == 0:
		observability.IncSeriesdetailFreshen("refreshed")
		h.cfg.Logger.InfoContext(parentCtx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", "refreshed"),
			slog.Int("sections_ok", total),
			slog.Int64("duration_ms", dur),
		)
		return seriesdetail.FreshenResult{Refreshed: true}, nil
	case failed < total:
		// Partial success: some sections committed, some errored.
		observability.IncSeriesdetailFreshen("partial_ok")
		h.cfg.Logger.WarnContext(parentCtx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", "partial_ok"),
			slog.Int("sections_ok", total-failed),
			slog.Int("sections_failed", failed),
			slog.String("failed_sections", failedSectionsList(errs)),
			slog.Int64("duration_ms", dur),
		)
		h.carryOverAsync(inner, seriesID, lang, errs, force)
		h.cfg.AsyncEnricher.EnqueueIfStale(seriesID, catalogseries.HydrationStub)
		return seriesdetail.FreshenResult{Refreshed: true, Degraded: true}, joinSectionErrors(errs)
	default:
		// All sections failed.
		label := "error"
		for _, e := range errs {
			if errors.Is(e.err, context.DeadlineExceeded) {
				label = "timeout"
				break
			}
		}
		observability.IncSeriesdetailFreshen(label)
		h.cfg.Logger.WarnContext(parentCtx, "freshen.run",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("result", label),
			slog.Int("sections_failed", failed),
			slog.String("failed_sections", failedSectionsList(errs)),
			slog.Int64("duration_ms", dur),
		)
		h.carryOverAsync(inner, seriesID, lang, errs, force)
		h.cfg.AsyncEnricher.EnqueueIfStale(seriesID, catalogseries.HydrationStub)
		return seriesdetail.FreshenResult{Degraded: true}, joinSectionErrors(errs)
	}
}

// dispatchAsync fires goroutines detached from caller ctx. Returns nil
// immediately. Per-section failures logged. Metric label 'refreshed'
// (optimistic — the goroutines will run to completion under 180s budget).
func (h *SeriesFreshenerHolder) dispatchAsync(
	inner SeriesWorkerHandle,
	seriesID domain.SeriesID,
	lang string,
	plan []dispatchItem,
	force bool,
	start time.Time,
) seriesdetail.FreshenResult {
	for _, item := range plan {
		h.spawnAsyncSection(inner, seriesID, lang, item, force)
	}
	observability.IncSeriesdetailFreshen("refreshed")
	h.cfg.Logger.InfoContext(context.Background(), "freshen.run",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
		slog.String("result", "async_dispatched"),
		slog.Int("sections_dispatched", len(plan)),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	return seriesdetail.FreshenResult{Refreshed: true}
}

// spawnAsyncSection fires ONE section on a detached ctx under
// asyncFollowupTimeout. Shared by dispatchAsync (initial async dispatch) and
// carryOverAsync (sync-budget carry-over). Fire-and-forget.
func (h *SeriesFreshenerHolder) spawnAsyncSection(
	inner SeriesWorkerHandle,
	seriesID domain.SeriesID,
	lang string,
	item dispatchItem,
	force bool,
) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), asyncFollowupTimeout)
		defer cancel()
		if err := h.runOne(ctx, inner, seriesID, lang, item, force); err != nil {
			observability.IncSeriesdetailFreshen("followup_error")
			h.cfg.Logger.WarnContext(ctx, "freshen.async_section",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("lang", lang),
				slog.String("section", string(item.section)),
				slog.String("result", "error"),
				slog.String("error", err.Error()),
			)
			return
		}
		observability.IncSeriesdetailFreshen("followup_ok")
		h.cfg.Logger.InfoContext(ctx, "freshen.async_section",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("section", string(item.section)),
			slog.String("result", "ok"),
		)
	}()
}

// carryOverAsync re-dispatches the sections that failed/timed-out under the
// sync budget onto the freshener's own 180s async path. This is the REAL
// "ModeSync-past-budget catch-up": the exact un-fetched sections are re-run
// via RefreshSeasonSlim/RefreshSeriesAllLangs/etc under asyncFollowupTimeout,
// NOT subject to the OnDemandEnricher 30s series-level throttle. Fire-and-forget.
func (h *SeriesFreshenerHolder) carryOverAsync(
	inner SeriesWorkerHandle,
	seriesID domain.SeriesID,
	lang string,
	errs []sectionError,
	force bool,
) {
	if len(errs) == 0 {
		return
	}
	for _, e := range errs {
		h.spawnAsyncSection(inner, seriesID, lang, dispatchItem{
			section: e.section,
			reason:  "sync_budget_carryover",
		}, force)
	}
	h.cfg.Logger.InfoContext(context.Background(), "freshen.carryover",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
		slog.Int("sections_carried", len(errs)),
		slog.String("sections", failedSectionsList(errs)),
	)
}

// runOne dispatches a single narrow method through singleflight per
// (seriesID, section, lang). Key format: "series-{id}:{section}:{lang}".
// `defer h.sf.Forget(key)` inside the SF closure so subsequent calls
// re-consult Probe (fresh verdicts might have changed).
func (h *SeriesFreshenerHolder) runOne(
	ctx context.Context,
	inner SeriesWorkerHandle,
	seriesID domain.SeriesID,
	lang string,
	item dispatchItem,
	force bool,
) error {
	key := fmt.Sprintf("series-%d:%s:%s", int64(seriesID), string(item.section), lang)
	_, err, _ := h.sf.Do(key, func() (any, error) {
		defer h.sf.Forget(key)
		return nil, h.invokeNarrow(ctx, inner, seriesID, lang, item, force)
	})
	return err
}

// invokeNarrow routes to the matching narrow Worker method. SectionSkeleton
// falls back to HandleForcedLang (no A2-A4 narrow method covers canon).
// SeasonSection(N) parsed via freshener.IsSeasonSection.
func (h *SeriesFreshenerHolder) invokeNarrow(
	ctx context.Context,
	inner SeriesWorkerHandle,
	seriesID domain.SeriesID,
	lang string,
	item dispatchItem,
	force bool,
) error {
	switch item.section {
	case freshener.SectionSkeleton:
		// Full canon Stage 1+2 sync commit (Story 546). No narrow method
		// covers series canonical fields (name, tmdb_id, first_air_date,
		// external_ids, status). Only fires on cold/never-synced case.
		return inner.HandleForcedLang(ctx, seriesID, lang)
	case freshener.SectionOverview:
		// S-B: all-langs in one call. The lang arg is intentionally dropped —
		// RefreshSeriesAllLangs writes every supported language.
		return inner.RefreshSeriesAllLangs(ctx, seriesID, force)
	case freshener.SectionCast:
		return inner.RefreshCast(ctx, seriesID, lang, force)
	case freshener.SectionRecommendations:
		return inner.RefreshRecommendations(ctx, seriesID, lang, force)
	case freshener.SectionMedia:
		return inner.RefreshMediaAssets(ctx, seriesID, lang, force)
	}
	if n, ok := freshener.IsSeasonSection(item.section); ok {
		return inner.RefreshSeasonSlim(ctx, seriesID, n, lang, force)
	}
	return fmt.Errorf("seriesfreshener: unknown section %q", string(item.section))
}

// dispatchItem — single (section, verdict) pair to dispatch.
type dispatchItem struct {
	section freshener.Section
	reason  string // Probe verdict reason, forwarded to narrow method logs
}

// sectionError bundles a section identity + narrow method error for
// combined-error handling.
type sectionError struct {
	section freshener.Section
	err     error
}

// planDispatch intersects caller-requested sections with Probe verdicts.
// force=true → dispatch every requested section unconditionally.
// force=false → dispatch only sections that Probe marks Stale=true.
//
// Preserves caller's section order (not Probe's FixedSections order) so
// composer-vs-changesync callers get deterministic dispatch traces.
func planDispatch(
	requested []freshener.Section,
	verdicts []freshener.SectionVerdict,
	force bool,
) []dispatchItem {
	if len(requested) == 0 {
		return nil
	}
	// Build lookup: section → verdict.
	verdictBy := make(map[freshener.Section]freshener.SectionVerdict, len(verdicts))
	for _, v := range verdicts {
		verdictBy[v.Section] = v
	}
	plan := make([]dispatchItem, 0, len(requested))
	for _, s := range requested {
		v, ok := verdictBy[s]
		switch {
		case force:
			reason := "forced"
			if ok {
				reason = "forced:" + v.Reason
			}
			plan = append(plan, dispatchItem{section: s, reason: reason})
		case ok && v.Stale:
			plan = append(plan, dispatchItem{section: s, reason: v.Reason})
		}
	}
	return plan
}

// mergeSeasonNumbers folds explicit seasonNumbers + any season:N Sections
// into a deduplicated slice for Probe. Callers can pass either explicit
// numbers (Phase 4 ChangesSyncer) OR season:N Sections (composer targeted
// season refresh) OR both (belt-and-suspenders).
func mergeSeasonNumbers(explicit []int, sections []freshener.Section) []int {
	seen := make(map[int]struct{}, len(explicit)+len(sections))
	out := make([]int, 0, len(explicit)+len(sections))
	for _, n := range explicit {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	for _, s := range sections {
		if n, ok := freshener.IsSeasonSection(s); ok {
			if _, dup := seen[n]; dup {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// failedSectionsList renders a stable comma-joined list of section names
// for log correlation. Order matches errs iteration (dispatch order).
func failedSectionsList(errs []sectionError) string {
	if len(errs) == 0 {
		return ""
	}
	names := make([]string, 0, len(errs))
	for _, e := range errs {
		names = append(names, string(e.section))
	}
	return joinStrings(names, ",")
}

// joinStrings — local strings.Join replacement to avoid pulling the
// strings import just for one comma-join (adapter package already
// carries many imports; readability-first).
func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	if len(ss) == 1 {
		return ss[0]
	}
	n := len(sep) * (len(ss) - 1)
	for _, s := range ss {
		n += len(s)
	}
	b := make([]byte, 0, n)
	b = append(b, ss[0]...)
	for _, s := range ss[1:] {
		b = append(b, sep...)
		b = append(b, s...)
	}
	return string(b)
}

// joinSectionErrors wraps errors.Join over the per-section errors slice,
// annotating each with its section for grep-friendly output. Empty →
// nil (caller checks len(errs) before calling but defensive anyway).
func joinSectionErrors(errs []sectionError) error {
	if len(errs) == 0 {
		return nil
	}
	wrapped := make([]error, 0, len(errs))
	for _, e := range errs {
		wrapped = append(wrapped, fmt.Errorf("section=%s: %w", string(e.section), e.err))
	}
	return errors.Join(wrapped...)
}
