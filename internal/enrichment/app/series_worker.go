package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SeriesWorkerDeps is the dependency surface — kept verbose
// (every repo is a named field, not a generic map) so a missing
// dependency surfaces as a nil-deref in the constructor's
// validate step, NOT inside the hot path under load.
type SeriesWorkerDeps struct {
	TMDB TMDBClient
	Tx   Transactor
	// Languages is the BCP-47 list the worker iterates over for every
	// enrichment pass. Empty → defaults to locale.SupportedUserLanguages
	// (the curated UI dropdown set, currently en-US + ru-RU). One row
	// per language is written into series_texts / episode_texts /
	// genres_i18n / keywords_i18n; one TMDB GetTV + per-active-season
	// GetSeason call is issued per language, so the per-series TMDB cost
	// is len(Languages)×.
	//
	// Story 533c: replaces the single-string Language field. PersonWorker
	// retains its single-language Language field because biographies
	// remain en-US-only until a follow-up story (cast / character
	// localisation is also deferred).
	Languages    []string
	Series       SeriesRepo
	SeriesTexts  SeriesTextsRepo
	Seasons      SeasonsRepo
	Episodes     EpisodesRepo
	EpisodeTexts EpisodeTextsRepo
	// SeasonTexts — B3b (Story 581): optional season-localization write
	// seam consumed by RefreshSeasonSlim. Writes one
	// season_texts.{series_id, season_number, lang} row from the SAME
	// GetSeason payload the method already fetched (no second TMDB call).
	// nil OK — when unset the season_texts step is skipped and the method
	// still writes episodes + episode_texts + the freshness stamp.
	// Production wiring (internal/wiring/enrichment.go) injects the shared
	// *persistence.SeasonTextsRepository. Field placed here (vs. the
	// required cluster below) deliberately so existing test fixtures that
	// construct SeriesWorkerDeps without it stay green — mirrors the
	// Probe / MediaResolver / RecCanonWriter nil-OK posture.
	SeasonTexts SeasonTextsRepo
	// SeriesMediaTexts — C-posters-A (Story 584a): optional per-language
	// poster/backdrop write port. Nil-OK — when nil, RefreshSeriesText
	// skips the per-lang media upsert and reads fall back to canon
	// series.poster_asset. Mirrors the SeasonTexts / MediaResolver nil-OK
	// posture. Production wiring injects
	// *enrichpersistence.SeriesMediaTextsRepository.
	SeriesMediaTexts SeriesMediaTextsRepo
	// SeasonMediaTexts — S-C2: optional per-language SEASON poster write port
	// consumed by RefreshSeasonSlim. Mirrors SeriesMediaTexts / SeasonTexts
	// nil-OK posture. Production impl is
	// *enrichpersistence.SeasonMediaTextsRepository.
	SeasonMediaTexts SeasonMediaTextsRepo
	People           PeopleRepo
	// PersonCredits — D-7 (468a): the series_worker writes
	// series-level credits into the polymorphic person_credits table
	// (media_type='tv', tmdb_media_id=<series.tmdb_id>) instead of the
	// dropped series_people table. Shares the port with PersonWorker
	// so the same DoUpdates AssignmentColumns shape (which avoids
	// SQLSTATE 42601 on Postgres) covers both write paths.
	PersonCredits PersonCreditsPort
	// PersonCreditsTexts — S-G: optional per-language cast character-name
	// write port consumed by RefreshCast. nil-OK — when nil, RefreshCast
	// skips the localized write and reads fall back to
	// person_credits.character_name. Mirrors the SeasonMediaTexts nil-OK
	// posture. Production impl is *enrichpersistence.PersonCreditsTextsRepository.
	PersonCreditsTexts PersonCreditsTextsPort
	// PeopleTexts — Story 1083: optional per-language person DISPLAY-name write
	// port consumed by RefreshCast. nil-OK — when nil, RefreshCast skips the
	// localized name write and the cast read-path falls back to
	// people.original_name (the terminal people.name tier was dropped in
	// migration 000037 — Story 1084b). Mirrors the PersonCreditsTexts nil-OK
	// posture. Production impl is *enrichpersistence.PeopleTextsRepository.
	PeopleTexts      PeopleTextsPort
	Genres           GenresRepo
	Keywords         KeywordsRepo
	Networks         NetworksRepo
	Companies        CompaniesRepo
	Videos           VideosRepoPort
	ContentRatings   ContentRatingsRepoPort
	ExternalIDs      ExternalIDsRepoPort
	Recommendations  RecommendationsRepoPort
	EnrichmentErrors EnrichmentErrorRepo
	// RecCanonWriter — Story 571 B-54. A3b RefreshRecommendations calls
	// UpdateRecCanonMedia after the series_texts.Upsert side-effect to
	// overwrite each rec child's canon poster_asset + backdrop_asset with
	// TMDB's lang-preferred paths from Recommendations.Results[*]. Nil-OK
	// — preserves pre-571 behavior where UpsertStub's COALESCE locked in
	// whatever poster path was first written (typically en-US from Sonarr
	// scan or first-hydration enrichment).
	RecCanonWriter SeriesRecCanonWriter
	MediaPrewarmer MediaPrewarmer // nil OK — F-1 not yet shipped
	// MediaResolver — A4: optional eager-hash + EnsurePending seam for
	// RefreshMediaAssets. Under Story 347 unified-resolve contract, calling
	// Resolve on a raw TMDB image path mints the deterministic sha256 hash
	// + writes a media_assets pending row inline, so the composer's next
	// cold /series/{id} read has a stable hash handle immediately (no async
	// gap where cold read sees NULL poster_hash). nil OK — RefreshMediaAssets
	// degrades to write raw paths + stamp only (production wiring passes the
	// shared *media.Resolver instance from cmd/server/server.go).
	MediaResolver MediaResolver
	// Dispatcher (212): post-tx enqueue seam for the person worker.
	// nil OK — keeps the existing test fixtures green; production
	// wiring passes the shared *DispatcherImpl.
	Dispatcher Dispatcher
	// OMDbBudget — W18-8: nil-OK non-consuming Cold-budget pre-check for the
	// imdb_id-gain OMDb enqueue. When nil the budget gate is skipped and the
	// OMDb worker's ReserveCold still gates downstream. Production wiring
	// (internal/wiring/enrichment.go) injects the shared *OMDbBudgetGuard.
	OMDbBudget OMDbBudget
	Logger     *slog.Logger
	Clock      func() time.Time // injected for tests; defaults to time.Now

	// SeasonConcurrency bounds the per-language parallel GetSeason fan-out
	// in refreshOneLanguage (Story 1096, Fix B). <1 → clamped to 1 at the
	// use site (sequential — matches pre-1096 behaviour). Production wiring
	// threads config.Enrichment.EnrichmentSeasonConcurrency (default 4).
	SeasonConcurrency int

	// Probe — A2: optional per-section freshness probe consumed by the
	// narrow refresh methods (RefreshSeriesText, RefreshCast) when
	// force=false. nil OK — narrow methods bypass the gate and fetch
	// unconditionally. Production wiring will inject the production
	// freshener.DBProbe alongside A5 EnsureFreshScope. Field added
	// here (vs. a new SeriesWorkerNarrowDeps struct) to keep the
	// constructor surface unchanged.
	Probe freshener.Probe
}

// SeriesWorker is the bound worker. Construct via NewSeriesWorker.
type SeriesWorker struct {
	deps SeriesWorkerDeps
}

// NewSeriesWorker validates that every required dependency is
// non-nil and returns the worker. Logger defaults to slog.Default;
// Clock defaults to time.Now; Languages defaults to
// locale.SupportedUserLanguages (the curated UI dropdown set).
func NewSeriesWorker(deps SeriesWorkerDeps) (*SeriesWorker, error) {
	if deps.TMDB == nil {
		return nil, errors.New("enrichment.series_worker: TMDB client required")
	}
	if deps.Tx == nil {
		return nil, errors.New("enrichment.series_worker: Transactor required")
	}
	if deps.Series == nil || deps.SeriesTexts == nil || deps.Seasons == nil ||
		deps.Episodes == nil || deps.EpisodeTexts == nil ||
		deps.People == nil || deps.PersonCredits == nil ||
		deps.Genres == nil || deps.Keywords == nil ||
		deps.Networks == nil || deps.Companies == nil ||
		deps.Videos == nil || deps.ContentRatings == nil ||
		deps.ExternalIDs == nil || deps.Recommendations == nil ||
		deps.EnrichmentErrors == nil {
		return nil, errors.New("enrichment.series_worker: every repository port is required")
	}
	if len(deps.Languages) == 0 {
		// Slice copy: the worker holds a defensive snapshot so a later
		// runtime reload that mutates locale.SupportedUserLanguages does
		// not race with an in-flight Handle.
		deps.Languages = append([]string(nil), locale.SupportedUserLanguages...)
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &SeriesWorker{deps: deps}, nil
}

// Handle is the dispatcher-facing entry point. seriesID is a CANON
// series.id (NOT series_cache.id). Returns an error only on a
// terminal failure that should NOT bubble (the worker journals
// outcome=error / not_found internally before returning).
func (w *SeriesWorker) Handle(ctx context.Context, seriesID domain.SeriesID) error {
	return w.handleInternal(ctx, seriesID, false)
}

// HandleForced is the Story 534 entry point used by the background
// refresh scheduler. Identical to Handle EXCEPT the freshness gate
// (canon.EnrichmentTMDBSyncedAt + TTL check) is bypassed — the
// scheduler's tiered TTL has already decided this series is stale,
// re-applying the worker's TTL here would short-circuit valid
// refreshes for series inside their per-tier window but outside the
// worker's 24h source TTL.
//
// Used ONLY by RefreshScheduler. All other callers (dispatcher,
// on-demand enricher, cold-start backfill) continue to use Handle so
// the in-band staleness check stays a server-side cache.
func (w *SeriesWorker) HandleForced(ctx context.Context, seriesID domain.SeriesID) error {
	return w.handleInternal(ctx, seriesID, true)
}

// HandleForcedLang is the Freshener-facing entry point for lang-targeted
// staged enrichment (Story 546).
//
// Diff vs HandleForced:
//  1. Single language (the one the user requested), NOT every entry in
//     w.deps.Languages.
//  2. STAGE 1+2 ONLY: one GetTV call. Series-level data committed in one
//     tx (canon + series_texts[lang] + season SHELLS + people +
//     person_credits(tv) + taxonomy + videos + content_ratings +
//     external_ids + recommendations). NO per-season GetSeason calls,
//     NO episodes write, NO episode_texts write, NO episode_credits.
//  3. Does NOT stamp enrichment_tmdb_synced_at — the
//     RefreshScheduler/Dispatcher-driven full Handle pass keeps that
//     contract. Background Worker.Handle via OnDemandEnricher dispatch
//     will fill episodes; not stamping here ensures the freshness gate
//     does NOT short-circuit that follow-up.
//
// Use case: read-through TMDB fallback (Freshener) where the user is on
// a 3s budget and wants Russian text NOW. The user-visible payload (hero
// title + season list + cast carousel + recommendations) is fully
// satisfied by Stage 1+2; episode lists land asynchronously via the
// separate per-season-detail endpoint after the dispatcher pass completes.
//
// Bug context: pre-546, the freshener invoked HandleForced which
// iterated EVERY w.deps.Languages entry AND fetched every active season's
// episode list per language. On a 9-season series this was 1 + 9 = 10
// TMDB calls × 2 langs = 20 calls under a 3s budget → ru-RU consistently
// tripped context.DeadlineExceeded → tx rollback → series_texts.ru-RU
// never written → 2h error backoff blocks retry → user stuck on en-US
// for hours. Concrete prod evidence: series 25551 "The Rookie",
// 2026-06-25 08:30 UTC, GetSeason(8,ru-RU) exceeded budget after 3000ms.
func (w *SeriesWorker) HandleForcedLang(ctx context.Context, seriesID domain.SeriesID, lang string) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceTMDBSeries)),
		slog.String("language", lang),
		slog.String("stage", "series_level"),
	)

	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.handle_lang.series_missing",
				slog.Int64("series_id", int64(seriesNF.ID)),
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("series worker (lang): load canon: %w", err)
	}
	if canon.TMDBID == nil {
		// Same Story 510 rationale as handleInternal — Sonarr-only series
		// with no tmdb_id can never be enriched via TMDB; log Debug + return.
		log.DebugContext(ctx, "enrichment.series.handle_lang.no_tmdb_id_skip")
		return nil
	}

	// Pull existing attempts counter for the backoff math on failure.
	prevAttempts := 0
	if errRow, errErr := w.deps.EnrichmentErrors.GetByEntitySource(ctx,
		enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceTMDBSeries); errErr == nil {
		prevAttempts = errRow.Attempts
	} else if !errors.Is(errErr, ports.ErrNotFound) {
		log.WarnContext(ctx, "enrichment.series.handle_lang.error_row_read_failed",
			slog.String("error", errErr.Error()))
	}

	// personsEnqueued/prewarmEnqueued are local single-lang flags — the
	// helper guards against double-firing on retry-within-same-call
	// (cannot happen on a single-lang call but the helper's signature
	// requires the pointer pair).
	personsEnqueued := false
	prewarmEnqueued := false
	omdbEnqueued := false
	if err := w.refreshOneLanguageStaged(ctx, canon, lang, &personsEnqueued, &prewarmEnqueued, &omdbEnqueued, log); err != nil {
		return w.handleTMDBError(ctx, seriesID, "lang-stage1="+lang, err, prevAttempts, start)
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	log.InfoContext(ctx, "enrichment.series.handle_lang.staged_ok",
		slog.String("language", lang),
		slog.Int("duration_ms", durMs),
		slog.String("note", "stage1_2_committed_episodes_pending_dispatcher"),
	)
	// W18-16: stamp the DEDICATED skeleton clock so the on-view SWR gate stops
	// re-firing this ~1.5s GetTV on every view. Best-effort (WARN on error): the
	// canon commit above is the source of truth; a missed stamp just means the
	// next view re-checks. Explicitly NOT MarkTMDBSynced — see below.
	if err := w.deps.Series.MarkSkeletonSynced(ctx, seriesID, w.deps.Clock()); err != nil {
		log.WarnContext(ctx, "enrichment.series.handle_lang.mark_skeleton_failed",
			slog.String("error", err.Error()))
	}
	// NB: deliberately NOT calling journalOK — see Story 546 decision #3.
	// The series-level data is committed, but the dispatcher-driven
	// full Handle pass (triggered via OnDemandEnricher.EnqueueIfStale by
	// the Freshener's success branch) still needs to fill episodes, and
	// that pass is TTL-gated. Stamping synced_at here would short-circuit
	// the follow-up.
	return nil
}

// refreshOneLanguageStaged is the Stage 1+2 clone of refreshOneLanguage.
// Diff: NO per-season GetSeason calls; seasonResponses is an empty map.
// applyAllForLanguage handles the empty map gracefully — episode-bearing
// steps (4 episodes / 5 episode_texts / 7b episode_credits) early-return
// or no-op when their input slice is empty.
//
// Returns the first non-nil error from GetTV / tx so HandleForcedLang
// can journal the failure.
func (w *SeriesWorker) refreshOneLanguageStaged(
	ctx context.Context,
	canon series.Canon,
	lang string,
	personsEnqueued *bool,
	prewarmEnqueued *bool,
	omdbEnqueued *bool,
	log *slog.Logger,
) error {
	// One TMDB call. GetTV bundles aggregate_credits + videos + images +
	// external_ids + content_ratings + keywords + recommendations via
	// append_to_response (see tmdb/tv.go:16), so everything the Stage 1+2
	// payload needs is in ONE round-trip.
	tv, err := w.deps.TMDB.GetTV(ctx, int64(*canon.TMDBID), lang)
	if err != nil {
		return fmt.Errorf("GetTV(%s): %w", lang, err)
	}

	// Empty seasons map → mapAllForLanguage produces zero episodes/
	// episode_texts; applyAllForLanguage's BatchUpsert(empty) and
	// applyEpisodeCredits(empty) no-op gracefully.
	emptySeasons := map[int]*tmdb.SeasonResponse{}
	mapped := w.mapAllForLanguage(tv, emptySeasons, canon, lang)

	var enqueueRows []personEnqueueRow
	var mergedCanon series.Canon
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		rows, merged, err := w.applyAllForLanguage(txCtx, canon, tv, emptySeasons, mapped, lang, log)
		if err != nil {
			return err
		}
		enqueueRows = rows
		mergedCanon = merged
		return nil
	})
	if err != nil {
		return fmt.Errorf("tx-staged(%s): %w", lang, err)
	}

	// Post-tx media prewarm. Asset paths come from canon + season shells,
	// which we DID persist this tx — safe to enqueue.
	if !*prewarmEnqueued && w.deps.MediaPrewarmer != nil {
		w.deps.MediaPrewarmer.Enqueue(ctx, mapped.PrewarmAssets)
		*prewarmEnqueued = true
	}

	// Post-tx person enqueue. aggregate_credits IS in tv (append_to_response)
	// so enqueueRows carries the series-level cast + crew the user sees in
	// the hero carousel. Episode-only guest stars are filled by the
	// dispatcher-driven full Handle pass.
	if !*personsEnqueued && w.deps.Dispatcher != nil {
		w.enqueuePersons(ctx, enqueueRows, log)
		*personsEnqueued = true
	}

	// W18-8 — cover the freshener staged path too: if this lang-targeted pass
	// is the first to write imdb_id, enqueue the OMDb Cold backfill.
	w.maybeEnqueueOMDbOnIMDBGain(ctx, canon, mergedCanon, omdbEnqueued, log)

	log.InfoContext(ctx, "enrichment.series.handle_lang.language_ok",
		slog.Int("seasons_fetched", 0),
		slog.Int("persons_enqueued", len(enqueueRows)),
	)
	return nil
}

// handleInternal carries the shared Handle/HandleForced body. The only
// behavioural diff vs the pre-534 Handle is the `if !force` guard
// around the freshness gate. Every other branch (no tmdb_id skip,
// per-language fan-out, error journal) is byte-identical.
func (w *SeriesWorker) handleInternal(ctx context.Context, seriesID domain.SeriesID, force bool) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceTMDBSeries)),
	)

	// 1. Read current canon — we need the tmdb_id to call TMDB AND
	//    the existing hydration level so MergeSeries lifts stub→full
	//    on success.
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.handle.series_missing",
				slog.Int64("series_id", int64(seriesNF.ID)),
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("series worker: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		// No tmdb_id — TMDB cannot enrich. This is a permanent natural
		// state for Sonarr-only imports (Sonarr does not always populate
		// tmdbId in /api/v3/series). Story 510 (B-38): we no longer
		// journal an enrichment_errors row here — the row would never
		// clear (no retry resolves it), would pollute the
		// ListDueForRetry/degraded[] consumers, and would inflate the
		// enrichment_errors_total metric. Cold-start scanner now filters
		// these rows at the SQL level (ListMissingTMDBSync WHERE
		// tmdb_id IS NOT NULL), so the only path reaching this branch in
		// steady state is operator-driven manual refresh — log at Debug
		// for diagnosis, return silently.
		log.DebugContext(ctx, "enrichment.series.handle.no_tmdb_id_skip")
		return nil
	}

	// 2. Staleness short-circuit — read canon.EnrichmentTMDBSyncedAt
	//    directly. nil = never enriched (proceed); within TTL = skip.
	//    Story 534: bypassed under force — the background scheduler
	//    has already gated this series as stale via the per-tier
	//    RefreshTTL; re-checking the worker's 24h source TTL here
	//    would silence valid refreshes for series inside their tier
	//    window but outside the source TTL.
	if !force && canon.EnrichmentTMDBSyncedAt != nil {
		ttl := enrichment.TTL(enrichment.SourceTMDBSeries, classifyKind(canon))
		if ttl > 0 && w.deps.Clock().Sub(*canon.EnrichmentTMDBSyncedAt) < ttl {
			log.DebugContext(ctx, "enrichment.series.handle.fresh_skip",
				slog.Time("synced_at", *canon.EnrichmentTMDBSyncedAt),
			)
			return nil
		}
	}

	// Load any current error row (for attempts counter on retry path).
	prevAttempts := 0
	if errRow, errErr := w.deps.EnrichmentErrors.GetByEntitySource(ctx,
		enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceTMDBSeries); errErr == nil {
		prevAttempts = errRow.Attempts
	} else if !errors.Is(errErr, ports.ErrNotFound) {
		log.WarnContext(ctx, "enrichment.series.handle.error_row_read_failed",
			slog.String("error", errErr.Error()))
	}

	// 3. Iterate every supported UI language. Each language gets its
	//    own independent transaction so a TMDB 5xx on lang B does not
	//    roll back lang A's successful write. First-success guards
	//    person enqueue + media pre-warm so we don't double-fire — the
	//    cast list and asset paths are language-INDEPENDENT.
	//
	//    Story 533c: SeriesWorkerDeps.Languages defaults to
	//    locale.SupportedUserLanguages (en-US + ru-RU); see PRD §5.5
	//    + the dual-lang fallback behaviour at
	//    enrichment/persistence/i18n_texts.go:21.
	personsEnqueued := false
	prewarmEnqueued := false
	omdbEnqueued := false
	failedLangs := make([]string, 0, len(w.deps.Languages))
	succeededLangs := make([]string, 0, len(w.deps.Languages))
	var firstErr error
	for _, lang := range w.deps.Languages {
		langLog := log.With(slog.String("language", lang))
		if err := w.refreshOneLanguage(ctx, canon, lang, force, &personsEnqueued, &prewarmEnqueued, &omdbEnqueued, langLog); err != nil {
			failedLangs = append(failedLangs, lang)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		succeededLangs = append(succeededLangs, lang)
	}

	// 4. Single freshness stamp — only on FULL success across every
	//    supported language. Partial success records an error row so the
	//    next dispatcher tick retries; stamping on partial success would
	//    silence that retry path.
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	if len(failedLangs) == 0 {
		w.journalOK(ctx, seriesID, now, durMs)
		log.InfoContext(ctx, "enrichment.series.handle.ok",
			slog.Any("languages", succeededLangs),
			slog.Int("duration_ms", durMs),
		)
		return nil
	}

	// Partial / total failure path: record one error row (source-level) +
	// log per-language detail. Reuse handleTMDBError for the backoff +
	// recordEnrichmentError plumbing; the first error wins.
	log.WarnContext(ctx, "enrichment.series.handle.partial_or_failed",
		slog.Any("succeeded_languages", succeededLangs),
		slog.Any("failed_languages", failedLangs),
		slog.Int("duration_ms", durMs),
		slog.String("first_error", firstErr.Error()),
	)
	return w.handleTMDBError(ctx, seriesID, "languages="+strings.Join(failedLangs, ","), firstErr, prevAttempts, start)
}

// refreshOneLanguage runs the full fetch + map + tx for a SINGLE BCP-47
// language. The first successful pass enqueues person workers + media
// prewarm; subsequent passes skip those side-effects (the cast list +
// poster assets are language-independent — see Story 533c design notes).
//
// Returns the first non-nil error from GetTV / GetSeason / tx so the
// caller can journal the failure. canon-row writes are repeated per
// language; the repeats are idempotent (ON CONFLICT DO UPDATE merges).
func (w *SeriesWorker) refreshOneLanguage(
	ctx context.Context,
	canon series.Canon,
	lang string,
	force bool,
	personsEnqueued *bool,
	prewarmEnqueued *bool,
	omdbEnqueued *bool,
	log *slog.Logger,
) error {
	// 3a. Fetch TV payload + active seasons in this language.
	tv, err := w.deps.TMDB.GetTV(ctx, int64(*canon.TMDBID), lang)
	if err != nil {
		return fmt.Errorf("GetTV(%s): %w", lang, err)
	}
	// Story 549: force=true (HandleForced / freshener-driven refresh)
	// uses firstHydration semantics so missing-lang population fetches
	// ALL seasons — otherwise old seasons (>365d) stay in en-US forever
	// when ru-RU is requested on an already-hydrated series.
	firstHydration := canon.EnrichmentTMDBSyncedAt == nil || force
	// Story 1096 (Fix B): bounded-parallel per-season fetch. The global
	// TMDB token-bucket limiter still serialises to 50 rps + adaptive
	// pause; fanning the loop out just fills the in-flight budget up to
	// SeasonConcurrency instead of one season at a time. seasonNeedsFetch
	// gating (incl. the season-0 skip on refresh) is preserved verbatim.
	seasonConcurrency := max(w.deps.SeasonConcurrency, 1)
	seasonResponses := make(map[int]*tmdb.SeasonResponse, len(tv.Seasons))
	var seasonMu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(seasonConcurrency)
	for _, s := range tv.Seasons {
		if !seasonNeedsFetch(s, firstHydration) {
			continue
		}
		s := s
		g.Go(func() error {
			sr, err := w.deps.TMDB.GetSeason(gctx, int64(*canon.TMDBID), s.SeasonNumber, lang)
			if err != nil {
				return fmt.Errorf("GetSeason(%d,%s): %w", s.SeasonNumber, lang, err)
			}
			seasonMu.Lock()
			seasonResponses[s.SeasonNumber] = sr
			seasonMu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// 3b. Map outside the tx, using THIS language for text-bearing
	//     fields. Pure CPU — no I/O — so a TMDB 5xx on a later language
	//     leaves zero half-written rows from this language.
	mapped := w.mapAllForLanguage(tv, seasonResponses, canon, lang)

	// 3c. ONE tx for THIS language's writes.
	var enqueueRows []personEnqueueRow
	var mergedCanon series.Canon
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		rows, merged, err := w.applyAllForLanguage(txCtx, canon, tv, seasonResponses, mapped, lang, log)
		if err != nil {
			return err
		}
		enqueueRows = rows
		mergedCanon = merged
		return nil
	})
	if err != nil {
		return fmt.Errorf("tx(%s): %w", lang, err)
	}

	// 3d. Media pre-warm — once per Handle (assets are
	//     language-independent: poster/backdrop/network logos/cast
	//     profiles do not vary per language). nil-OK enqueue.
	if !*prewarmEnqueued && w.deps.MediaPrewarmer != nil {
		w.deps.MediaPrewarmer.Enqueue(ctx, mapped.PrewarmAssets)
		*prewarmEnqueued = true
	}

	// 3e. Enqueue persons post-tx — once per Handle. Calling
	//     Dispatcher.Enqueue inside the tx is a layering violation; the
	//     dispatcher's dedup handles double-enqueues, but skipping the
	//     second-language enqueue keeps the log line honest about
	//     persons_enqueued and saves the dispatcher's dedup work.
	if !*personsEnqueued && w.deps.Dispatcher != nil {
		w.enqueuePersons(ctx, enqueueRows, log)
		*personsEnqueued = true
	}

	// 3f. W18-8 — enqueue an OMDb Cold fetch on the imdb_id null→value
	//     transition (once per Handle). Passive backfill so the next on-view
	//     already has IMDb rating/awards/rated. Guards (fresh-TTL /
	//     terminal-negative / Cold-budget) live in maybeEnqueueOMDbOnIMDBGain.
	w.maybeEnqueueOMDbOnIMDBGain(ctx, canon, mergedCanon, omdbEnqueued, log)

	log.InfoContext(ctx, "enrichment.series.handle.language_ok",
		slog.Int("seasons_fetched", len(seasonResponses)),
		slog.Int("persons_enqueued", len(enqueueRows)),
	)
	return nil
}

// personEnqueueRow pairs a resolved canon person.id with the TMDB
// billing position from the cast row. CreditOrder=999 is the crew
// sentinel — crew has no billing order, so it ALWAYS lands in the
// cold bucket.
type personEnqueueRow struct {
	PersonID    int64
	CreditOrder int
}

// enqueuePersons fans rows out to the dispatcher with hot/cold split.
// credit_order < 10 → hot; ≥10 (including crew sentinel 999) → cold.
// The dispatcher's dedup handles double-enqueues for the same person.
func (w *SeriesWorker) enqueuePersons(ctx context.Context, rows []personEnqueueRow, log *slog.Logger) {
	_ = ctx
	const topN = 10
	hot, cold := 0, 0
	for _, r := range rows {
		p := PriorityCold
		if r.CreditOrder < topN {
			p = PriorityHot
			hot++
		} else {
			cold++
		}
		w.deps.Dispatcher.Enqueue(EntityPerson, r.PersonID, p)
	}
	log.Debug("enrichment.series.handle.persons_enqueued",
		slog.Int("hot", hot),
		slog.Int("cold", cold),
	)
}

// maybeEnqueueOMDbOnIMDBGain enqueues one OMDb Cold fetch when a series' canon
// imdb_id transitions empty→value on this Handle. Called post-tx (Enqueue
// inside a tx is a layering violation). The transition test compares the OLD
// canon (Handle-start snapshot, stable across the language loop) against the
// merged canon this language pass just persisted; a re-enrichment of an
// already-imdb_id'd series does NOT fire (imdbWasEmpty is false). The
// omdbEnqueued pointer gates to once-per-Handle: it trips the first time the
// transition is observed on a committed language pass, regardless of the guard
// outcome (the guards do not change within one Handle).
func (w *SeriesWorker) maybeEnqueueOMDbOnIMDBGain(ctx context.Context, oldCanon, merged series.Canon, omdbEnqueued *bool, log *slog.Logger) {
	if omdbEnqueued == nil || *omdbEnqueued || w.deps.Dispatcher == nil {
		return
	}
	imdbWasEmpty := oldCanon.IMDBID == nil || *oldCanon.IMDBID == ""
	imdbNowSet := merged.IMDBID != nil && *merged.IMDBID != ""
	if !imdbWasEmpty || !imdbNowSet {
		return // no null→value transition this Handle
	}
	// Transition observed — evaluate once per Handle regardless of outcome.
	*omdbEnqueued = true
	if merged.ID == 0 {
		return
	}
	if !w.shouldEnqueueOMDbCold(ctx, merged, log) {
		return
	}
	w.deps.Dispatcher.Enqueue(EntityOMDb, int64(merged.ID), PriorityCold)
	log.InfoContext(ctx, "enrichment.omdb.enqueued",
		slog.Int64("series_id", int64(merged.ID)),
		slog.String("imdb_id", string(*merged.IMDBID)),
		slog.String("priority", priorityLabel(PriorityCold)),
		slog.String("trigger", "imdb_id_gained"),
	)
}

// shouldEnqueueOMDbCold runs the anti-hammer guards for the W18-8 imdb_id-gain
// enqueue. All checks are cheap reads on the just-merged canon + the durable
// negative-cache; none consume budget (the real ReserveCold spend stays in the
// OMDb worker). Returns false — skip silently — when OMDb is already fresh, the
// series is terminal-negative, or the Cold budget is at the Hot floor.
func (w *SeriesWorker) shouldEnqueueOMDbCold(ctx context.Context, canon series.Canon, log *slog.Logger) bool {
	// Guard 1: OMDb already fresh by the W18-5 progressive TTL → skip.
	if canon.EnrichmentOMDBSyncedAt != nil {
		kind := classifyOMDbKind(canon, w.deps.Clock())
		ttl := enrichment.TTL(enrichment.SourceOMDb, kind)
		if ttl > 0 && w.deps.Clock().Sub(*canon.EnrichmentOMDBSyncedAt) < ttl {
			log.DebugContext(ctx, "enrichment.omdb.enqueue_skip_fresh",
				slog.Int64("series_id", int64(canon.ID)),
				slog.String("ttl_kind", string(kind)),
			)
			return false
		}
	}

	// Guard 2: terminal-negative (attempts > 5) → skip. Parity with the daily
	// batch selector's SQL give-up filter (F-09 durable negative-cache).
	if errRow, err := w.deps.EnrichmentErrors.GetByEntitySource(ctx,
		enrichment.EntityTypeSeries, int64(canon.ID), enrichment.SourceOMDb); err == nil {
		if errRow.Attempts > omdbColdEnqueueTerminalAttempts {
			log.DebugContext(ctx, "enrichment.omdb.enqueue_skip_terminal",
				slog.Int64("series_id", int64(canon.ID)),
				slog.Int("attempts", errRow.Attempts),
			)
			return false
		}
	} else if !errors.Is(err, ports.ErrNotFound) {
		log.WarnContext(ctx, "enrichment.omdb.enqueue_error_row_read_failed",
			slog.Int64("series_id", int64(canon.ID)),
			slog.String("error", err.Error()),
		)
	}

	// Guard 3: Cold budget at the Hot floor → skip (non-consuming). nil budget
	// (test fixtures / not-yet-wired) leaves the gate to the OMDb worker's
	// ReserveCold. On-view Hot or the next trigger will retry.
	if w.deps.OMDbBudget != nil && !w.deps.OMDbBudget.ColdAvailable() {
		log.DebugContext(ctx, "enrichment.omdb.enqueue_skip_budget",
			slog.Int64("series_id", int64(canon.ID)),
			slog.Int("quota_remaining", w.deps.OMDbBudget.Remaining()),
		)
		return false
	}

	return true
}

// classifyKind picks the TTL bucket from the canon's Status /
// InProduction fields. Accepts BOTH vocabularies that can land in
// series.status: TMDB's canonical case ("Returning Series",
// "In Production", "Pilot", "Planned") AND Sonarr's coarse lowercase
// ("continuing"), which sonarr_sync writes as a fallback for tmdb-less
// rows. Any of these → KindSeriesContinuing; everything else
// (including Sonarr's "ended"/"deleted" and TMDB's "Ended"/"Canceled")
// → KindSeriesEnded. (PRD §5.5 TTL matrix.)
func classifyKind(c series.Canon) enrichment.Kind {
	if c.InProduction {
		return enrichment.KindSeriesContinuing
	}
	if c.Status == nil {
		return enrichment.KindSeriesEnded
	}
	switch *c.Status {
	case "Returning Series", "In Production", "Pilot", "Planned", "continuing":
		return enrichment.KindSeriesContinuing
	}
	return enrichment.KindSeriesEnded
}

// seasonNeedsFetch — first hydration (firstHydration=true) ⇒ fetch
// ALL seasons; refresh ⇒ fetch ONLY active seasons (latest season +
// any with unaired episodes). Story 211 ships the simple heuristic
// "fetch all on first sync, fetch latest on refresh"; finer
// per-season granularity is post-D-7 polish.
func seasonNeedsFetch(s tmdb.TVSeasonStub, firstHydration bool) bool {
	if firstHydration {
		return true
	}
	// On refresh: skip season 0 (specials, mostly static) and
	// closed seasons. Heuristic: season with no AirDate OR AirDate
	// > 365d ago AND we already have it ⇒ skip.
	if s.SeasonNumber == 0 {
		return false
	}
	if s.AirDate == "" {
		return true
	}
	t, err := time.Parse("2006-01-02", s.AirDate)
	if err != nil {
		return true
	}
	return time.Since(t) < 365*24*time.Hour
}

// mappedPayload bundles every value the worker needs inside the tx.
// Built in mapAll — pure-CPU; no I/O.
type mappedPayload struct {
	SeriesPatch     enrichment.SeriesPatch
	SeriesText      series.SeriesText
	Seasons         []series.CanonSeason
	Episodes        []series.CanonEpisode // canonical, fully-merged
	EpisodeTexts    []series.EpisodeText
	PersonStubs     []people.Person       // unique stubs from TV credits
	SeriesCredits   []people.SeriesCredit // SeriesID set; PersonID filled inside tx
	Genres          []taxonomy.Genre
	Keywords        []taxonomy.Keyword
	Networks        []taxonomy.Network
	Companies       []taxonomy.ProductionCompany
	Videos          []VideoRow
	ContentRatings  []tmdb.MappedContentRating
	ExternalIDs     []tmdb.MappedExternalID
	Recommendations []series.Canon // hydration=stub
	// PrewarmAssets is the per-series media-prewarm payload built by
	// mapAll. The series_worker hands the slice to MediaPrewarmer
	// AFTER the tx commits.
	PrewarmAssets []MediaPrewarmRequest
}

// mapAllForLanguage runs every mapper from infrastructure/tmdb against
// the payload + composes the SeriesPatch + collects pre-warm asset
// list. Pure — no logger, no errors (the mappers are fallible over JSON
// parse, but every mapper here is total).
//
// Story 533c: lang is the BCP-47 tag the caller is currently iterating;
// it is stamped into the SeriesText + EpisodeText rows so the per-
// language Upsert produces ONE row per (entity_id, language).
// Language-INDEPENDENT fields (canon patch, seasons, credits, taxonomy
// IDs, videos, content_ratings, external_ids, recommendations, prewarm
// assets) are mapped identically every call; the repeated writes are
// idempotent (ON CONFLICT DO UPDATE), so the second-language tx is a
// near-no-op for those tables.
func (w *SeriesWorker) mapAllForLanguage(tv *tmdb.TVResponse, seasons map[int]*tmdb.SeasonResponse, canon series.Canon, lang string) mappedPayload {
	out := mappedPayload{}

	tmdbCanon := tmdb.MapTVToCanon(tv)
	out.SeriesPatch = patchFromTMDBCanon(tmdbCanon)
	out.SeriesText = series.SeriesText{
		SeriesID: canon.ID, Language: lang,
		Title: nonEmptyStringPtr(tv.Name), Overview: nonEmptyStringPtr(tv.Overview), Tagline: nonEmptyStringPtr(tv.Tagline),
	}

	out.Seasons = tmdb.MapTVToSeasons(tv)
	for i := range out.Seasons {
		out.Seasons[i].SeriesID = canon.ID
	}

	credits, stubs := tmdb.MapTVToCredits(tv)
	for i := range credits {
		credits[i].SeriesID = canon.ID
	}
	out.SeriesCredits = credits
	out.PersonStubs = stubs

	g, k, n, c := tmdb.MapTVToTaxonomy(tv)
	out.Genres, out.Keywords, out.Networks, out.Companies = g, k, n, c

	for _, v := range tmdb.MapTVToVideos(tv) {
		out.Videos = append(out.Videos, VideoRow{
			SeriesID: canon.ID, TMDBID: v.TMDBID,
			Language: v.Language, Country: v.Country,
			Name: v.Name, Key: v.Key, Site: v.Site,
			Type: v.Type, Official: v.Official,
			PublishedAt: v.PublishedAt, Size: v.Size,
		})
	}
	out.ContentRatings = tmdb.MapTVToContentRatings(tv)
	out.ExternalIDs = tmdb.MapTVToExternalIDs(tv)
	out.Recommendations = tmdb.MapTVToRecommendations(tv)

	// Episodes: walk every fetched season + accumulate canon
	// episodes + episode_texts. The tx-time resolver wires the
	// fresh season_id onto each episode.
	for _, sr := range seasons {
		seasonEps := tmdb.MapSeasonToEpisodes(sr, canon.ID, 0 /* resolved in tx */)
		for _, e := range seasonEps {
			out.Episodes = append(out.Episodes, e)
			if sr != nil {
				// One episode_text per episode keyed by `lang` (Story
				// 533c). Episode IDs are resolved post-batch upsert;
				// the text rows use a sentinel zero EpisodeID that
				// applyAllForLanguage patches.
				out.EpisodeTexts = append(out.EpisodeTexts, series.EpisodeText{
					Language: lang,
					Title:    nonEmptyStringPtr(findEpisodeTitle(sr, e.SeasonNumber, e.EpisodeNumber)),
					Overview: nonEmptyStringPtr(findEpisodeOverview(sr, e.SeasonNumber, e.EpisodeNumber)),
				})
			}
		}
	}

	// Pre-warm payload — PRD §6.4. Build full image URLs from the
	// raw TMDB paths the mapper persisted on canon entities. Order:
	// poster (w342 grid + w780 hero), backdrop w1280, network logos
	// w185, top-10 cast profiles w185, season posters w342, one
	// trailer thumbnail (best-effort YouTube hqdefault).
	out.PrewarmAssets = composePrewarmAssets(canon, out, tv)
	return out
}

// applyAllForLanguage runs every repo write inside ONE tx. Order is the
// deterministic dependency-sorted sequence from PRD §5.6:
// canon → texts → seasons → episodes → episode_texts →
// people → series_people → taxonomy → videos → content_ratings →
// external_ids → recommendations.
//
// Returns the resolved person enqueue rows so the caller (refreshOneLanguage)
// can fan them out to the dispatcher AFTER the tx commits (calling
// Dispatcher.Enqueue inside a tx is a layering violation — the
// dispatcher's dedup window opens at enqueue time, not at commit).
//
// Story 533c: lang is threaded through to applyTaxonomyForLanguage so
// genres_i18n / keywords_i18n rows are written keyed by THIS language
// (TMDB returns localised genre/keyword names per ?language=X). The
// series_texts + episode_texts rows already carry Language=lang via
// the mapping step.
func (w *SeriesWorker) applyAllForLanguage(txCtx context.Context, canon series.Canon, tv *tmdb.TVResponse, seasons map[int]*tmdb.SeasonResponse, m mappedPayload, lang string, log *slog.Logger) ([]personEnqueueRow, series.Canon, error) {
	// 1. Merge + upsert series canon.
	merged := enrichment.MergeSeries(
		canonToEnrichmentCanon(canon),
		m.SeriesPatch,
		enrichment.SourceTMDBSeries,
	)
	canonOut := enrichmentCanonToCanon(merged, canon)

	// S-E3a — the 346 canon poster/backdrop write-gap guard + the
	// images_persisted diagnostic were removed: canon no longer carries
	// poster_asset / backdrop_asset. Series art is persisted per-language
	// by RefreshMediaAssets (A4) into series_media_texts.
	seriesID, err := w.deps.Series.Upsert(txCtx, canonOut)
	if err != nil {
		return nil, series.Canon{}, fmt.Errorf("upsert series canon: %w", err)
	}
	// W18-8: pin the persisted id onto the returned canon so the post-tx
	// caller enqueues against the real series id (canonOut.ID is preserved
	// from the base canon, but Upsert is the authoritative id for new rows).
	canonOut.ID = seriesID

	// 2. series_texts.
	m.SeriesText.SeriesID = seriesID
	if err := w.deps.SeriesTexts.Upsert(txCtx, m.SeriesText); err != nil {
		return nil, series.Canon{}, fmt.Errorf("upsert series_texts: %w", err)
	}

	// 2b. series_media_texts — per-language poster/backdrop seed.
	//     S-E3b dropped canon series.poster_asset/backdrop_asset; the art now
	//     lives ONLY in this side-table. The always-on canon-sync path must seed
	//     it or a library series has NO poster on first view: the skeleton
	//     composer freshens only SectionSkeleton (→ this path), never the
	//     SectionOverview all-langs writer nor SectionMedia, so nothing else
	//     writes series_media_texts for a Sonarr-added series. pickPosterForLang
	//     picks the best per-lang image with a root-poster fallback — the same
	//     source that populated canon.poster_asset pre-S-E3b. Raw paths only:
	//     the hero + grid resolve the media hash at read time, and
	//     MediaResolver.Resolve is deliberately NOT called inside this multi-
	//     table tx (it opens its own media_assets session → cross-table lock
	//     contention risk, per the RefreshSeriesAllLangs pattern). The
	//     COALESCE-protected Upsert preserves any hash a later all-langs/A4
	//     refresh fills in.
	if w.deps.SeriesMediaTexts != nil {
		// Story 1081a — the cold-view seed must agree with the strict media
		// writers or it re-poisons ru rows with the en root poster and clears
		// the plain-excluded presence markers. Base → full pick + root fallback;
		// NON-base → strict exact-lang only (absence row when TMDB has no ru
		// poster). Always write the row (persist the absence) + stamp *_checked_at.
		var posterPath, backdropPath *string
		if lang == locale.Default() {
			posterPath = pickPosterForLang(tv.Images, lang)
			if posterPath == nil {
				posterPath = nonEmptyStringPtr(tv.PosterPath)
			}
			backdropPath = pickBackdropForLang(tv.Images, lang)
			if backdropPath == nil {
				backdropPath = nonEmptyStringPtr(tv.BackdropPath)
			}
		} else {
			posterPath = pickPosterForLangStrict(tv.Images, lang)
			backdropPath = pickBackdropForLang(tv.Images, lang)
			if backdropPath == nil {
				backdropPath = nonEmptyStringPtr(tv.BackdropPath)
			}
		}
		now := w.deps.Clock()
		if err := w.deps.SeriesMediaTexts.Upsert(txCtx, series.SeriesMediaText{
			SeriesID:          seriesID,
			Language:          lang,
			PosterAsset:       posterPath,
			BackdropAsset:     backdropPath,
			EnrichedAt:        &now,
			PosterCheckedAt:   &now,
			BackdropCheckedAt: &now,
		}); err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert series_media_texts: %w", err)
		}
	}

	// 3. Seasons — upsert each + collect (number → season_id) for
	//    episode wiring.
	seasonIDByNumber := make(map[int]int64, len(m.Seasons))
	for _, s := range m.Seasons {
		s.SeriesID = seriesID
		id, err := w.deps.Seasons.Upsert(txCtx, s)
		if err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert season %d: %w", s.SeasonNumber, err)
		}
		seasonIDByNumber[s.SeasonNumber] = id
	}

	// 4. Episodes — wire season_id, batch upsert.
	for i := range m.Episodes {
		m.Episodes[i].SeriesID = seriesID
		if sid, ok := seasonIDByNumber[m.Episodes[i].SeasonNumber]; ok {
			m.Episodes[i].SeasonID = &sid
		}
	}
	episodeIDs, err := w.deps.Episodes.BatchUpsert(txCtx, m.Episodes)
	if err != nil {
		return nil, series.Canon{}, fmt.Errorf("batch upsert episodes: %w", err)
	}

	// 5. episode_texts — wire by parallel index from BatchUpsert.
	for i, txt := range m.EpisodeTexts {
		if i >= len(episodeIDs) {
			break
		}
		txt.EpisodeID = domain.EpisodeID(episodeIDs[i])
		if err := w.deps.EpisodeTexts.Upsert(txCtx, txt); err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert episode_texts: %w", err)
		}
	}

	// 6. People stubs — upsert each, build (tmdb_person_id → person_id) map.
	//
	// B-26: PersonStubs is sorted by tmdb_id ASC before the loop so that
	// two parallel series_worker txes acquire row-level locks on `people`
	// in the same global order — no cross-tx deadlock cycle possible.
	// Mapper-built order (TMDB credits payload order) is not stable across
	// series, which historically produced SQLSTATE 40P01 victims.
	// In-place sort is safe: mappedPayload is not reused after this tx.
	slices.SortStableFunc(m.PersonStubs, func(a, b people.Person) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})

	personIDByTMDB := make(map[int]int64, len(m.PersonStubs))
	for _, st := range m.PersonStubs {
		pid, err := w.deps.People.Upsert(txCtx, st)
		if err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert person stub: %w", err)
		}
		if st.TMDBID != nil {
			personIDByTMDB[int(*st.TMDBID)] = pid
		}
	}

	// 6b. Story 1090 — per-person max(season_number) for the last_appearance
	//     cast sort. Walk each fetched season's aggregate_credits.cast and
	//     record the highest REAL season (skip season 0 specials) each person
	//     appears in, keyed by canon person_id (resolved via personIDByTMDB).
	//     Threaded into the media_type='tv' person_credits write below; the
	//     writer MAX-merges it against the stored value so an unfetched older
	//     season on a partial refresh never regresses a higher stored value.
	lastAppByPerson := make(map[int64]int, len(personIDByTMDB))
	for _, sr := range seasons {
		if sr == nil || sr.SeasonNumber == 0 || sr.AggregateCredits == nil {
			continue
		}
		for _, cast := range sr.AggregateCredits.Cast {
			pid, ok := personIDByTMDB[int(cast.ID)]
			if !ok || pid == 0 {
				continue
			}
			if sr.SeasonNumber > lastAppByPerson[pid] {
				lastAppByPerson[pid] = sr.SeasonNumber
			}
		}
	}

	// 7. person_credits (media_type='tv') — re-walk the TV
	//    aggregate_credits payload to pair each credit row with the
	//    TMDB person id, then resolve against personIDByTMDB. The
	//    mapper-produced SeriesCredit slice does NOT carry the TMDB
	//    person id (see infrastructure/tmdb/mappers.go::MapTVToCredits
	//    comment); re-walking is the cheapest correlation path that
	//    does NOT change the mapper surface. Credits whose person we
	//    failed to upsert (defensive) are dropped + counted.
	//
	//    D-7 (468a): series-level credits land on the polymorphic
	//    person_credits table (media_type=MediaTypeTV, tmdb_media_id=
	//    canon.tmdb_id) — the legacy series_people table was dropped
	//    in D-1. MediaTypeTV is the SAME discriminator the
	//    PersonWorker uses for /person/{id}/tv_credits rows, so the
	//    UNIQUE (person_id, tmdb_credit_id) UPSERT branches harmonise
	//    across both writers (no UPDATE-thrash between media types).
	finalCredits, droppedCredits := resolveSeriesCreditsWithPersonID(tv, seriesID, personIDByTMDB)
	if len(finalCredits) > 0 {
		// canon.TMDBID is non-nil here: Handle guards against nil
		// before invoking applyAll, so the deref is safe. The shape
		// the port expects ([]people.PersonCredit) is built in-place
		// by mapSeriesCreditsToPersonCredits.
		pcRows := mapSeriesCreditsToPersonCredits(finalCredits, tv, int64(*canon.TMDBID), lastAppByPerson)
		if _, err := w.deps.PersonCredits.BatchUpsert(txCtx, pcRows); err != nil {
			return nil, series.Canon{}, fmt.Errorf("batch upsert person_credits (tv): %w", err)
		}
	}
	if droppedCredits > 0 {
		log.WarnContext(txCtx, "enrichment.series.handle.credits_dropped",
			slog.Int("dropped", droppedCredits),
			slog.Int("kept", len(finalCredits)),
		)
	}

	// 7b. person_credits (media_type='tv_episode') — D-7 (468b): walk
	//     every hydrated season's per-episode guest_stars + crew and
	//     project them onto the polymorphic person_credits surface
	//     (tmdb_media_id=<episode tmdb_id>). Replaces the dropped
	//     `episode_people` table; the natural key
	//     (person_id, tmdb_credit_id) keeps re-ingest idempotent.
	//
	//     Person-stub upsert: season guest_stars are NOT included in the
	//     series-level aggregate_credits payload, so personIDByTMDB
	//     (built in step 6) does not contain them. The worker upserts
	//     a stub Person for each unseen tmdb_id here so the FK target
	//     exists by the time BatchUpsert runs. Same UpsertStub-equivalent
	//     shape PeopleRepo.Upsert already provides (idempotent on tmdb_id).
	//
	//     Person stubs whose Upsert fails are surfaced as drops — the
	//     batch loses that credit row but the tx keeps going. The seed
	//     for /api/v1/instances/{name}/series/{id} routes does not yet
	//     read episode credits (B-13 hero stops at series-level cast),
	//     so the write is a "build it so the read can land in N-6"
	//     posture rather than a hot-path. Loss is acceptable.
	episodeCreditRows, droppedEpisodeCredits, err := w.applyEpisodeCredits(txCtx, seasons, personIDByTMDB, log)
	if err != nil {
		return nil, series.Canon{}, fmt.Errorf("apply episode credits: %w", err)
	}
	if len(episodeCreditRows) > 0 {
		if _, err := w.deps.PersonCredits.BatchUpsert(txCtx, episodeCreditRows); err != nil {
			return nil, series.Canon{}, fmt.Errorf("batch upsert person_credits (tv_episode): %w", err)
		}
	}
	if droppedEpisodeCredits > 0 {
		log.WarnContext(txCtx, "enrichment.series.handle.episode_credits_dropped",
			slog.Int("dropped", droppedEpisodeCredits),
			slog.Int("kept", len(episodeCreditRows)),
			slog.String("reason", "guest_star person stub upsert failed or credit_id empty"),
		)
	}

	// 7c. 212: build the post-tx person-enqueue list from the cast +
	//     crew rows. Cast carries credit_order (TMDB billing index) —
	//     the worker passes <10 to PriorityHot, ≥10 to PriorityCold
	//     downstream. Crew has no billing order; sentinel 999 forces
	//     cold for every crew row.
	enqueueRows := make([]personEnqueueRow, 0, len(personIDByTMDB))
	if tv.AggregateCredits != nil {
		for _, cast := range tv.AggregateCredits.Cast {
			pid, ok := personIDByTMDB[int(cast.ID)]
			if !ok || pid == 0 {
				continue
			}
			enqueueRows = append(enqueueRows, personEnqueueRow{
				PersonID:    pid,
				CreditOrder: cast.Order,
			})
		}
		for _, crew := range tv.AggregateCredits.Crew {
			pid, ok := personIDByTMDB[int(crew.ID)]
			if !ok || pid == 0 {
				continue
			}
			enqueueRows = append(enqueueRows, personEnqueueRow{
				PersonID:    pid,
				CreditOrder: 999,
			})
		}
	}

	// 8. Taxonomy — Genres / Keywords / Networks / Companies upsert + Set.
	//    Story 533c: lang threads through so genres_i18n / keywords_i18n
	//    rows are written keyed by THIS language.
	if err := w.applyTaxonomyForLanguage(txCtx, seriesID, m, lang); err != nil {
		return nil, series.Canon{}, err
	}

	// 9. Videos.
	for _, v := range m.Videos {
		v.SeriesID = seriesID
		if err := w.deps.Videos.Upsert(txCtx, v); err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert video: %w", err)
		}
	}

	// 10. Content ratings.
	for _, cr := range m.ContentRatings {
		if err := w.deps.ContentRatings.Upsert(txCtx, seriesID, cr.Country, cr.Rating); err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert content_rating: %w", err)
		}
	}

	// 11. External IDs.
	for _, e := range m.ExternalIDs {
		if err := w.deps.ExternalIDs.Upsert(txCtx, enrichment.EntityTypeSeries, int64(seriesID), e.Provider, e.ProviderID); err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert external_id: %w", err)
		}
	}

	// 12. Recommendations — upsert each stub by tmdb_id, collect
	//     canon ids, write the join via Set. Story 319: stubs go
	//     through UpsertStub, whose ON CONFLICT preserves existing
	//     poster_asset / backdrop_asset / hydration='full' so a
	//     recommendation sweep cannot blank out a real canon row.
	//
	// B-26: UpsertStub touches the `series` table (partial unique on
	// tmdb_id). Two parallel series_worker txes upserting overlapping
	// recommendation stubs without ordering produce SQLSTATE 40P01
	// (`upsert recommendation stub: upsert stub series: deadlock detected`).
	// Discipline: sort a COPY by tmdb_id ASC for the upsert loop (global
	// lock order); emit recIDs in the ORIGINAL TMDB-rank order so
	// `recommendations.position` still reflects TMDB ranking
	// (recommendations have a user-visible order; people/genres do not).
	sortedRecs := make([]series.Canon, len(m.Recommendations))
	copy(sortedRecs, m.Recommendations)
	slices.SortStableFunc(sortedRecs, func(a, b series.Canon) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})
	stubIDByTMDB := make(map[domain.TMDBID]domain.SeriesID, len(sortedRecs))
	for _, rec := range sortedRecs {
		id, err := w.deps.Series.UpsertStub(txCtx, rec)
		if err != nil {
			return nil, series.Canon{}, fmt.Errorf("upsert recommendation stub: %w", err)
		}
		if rec.TMDBID != nil {
			stubIDByTMDB[*rec.TMDBID] = id
		}
	}
	// Emit recIDs in ORIGINAL (TMDB-rank) order, dropping self-refs and
	// any stub whose upsert was suppressed (TMDBID==nil — should be
	// impossible because UpsertStub validates that, but defensive).
	recIDs := make([]domain.SeriesID, 0, len(m.Recommendations))
	for _, rec := range m.Recommendations {
		if rec.TMDBID == nil {
			continue
		}
		id, ok := stubIDByTMDB[*rec.TMDBID]
		if !ok {
			continue
		}
		// Skip self-references defensively (the recommendations Set
		// rejects recommended_series_id == series_id).
		if id == seriesID {
			continue
		}
		recIDs = append(recIDs, id)
	}
	if err := w.deps.Recommendations.Set(txCtx, seriesID, recIDs); err != nil {
		return nil, series.Canon{}, fmt.Errorf("set recommendations: %w", err)
	}
	return enqueueRows, canonOut, nil
}

// mapSeriesCreditsToPersonCredits projects the per-series cast+crew
// slice that resolveSeriesCreditsWithPersonID returns into the
// polymorphic []people.PersonCredit shape PersonCreditsPort wants.
// D-7 (468a): MediaType is hard-wired to tmdb.MediaTypeTV — the same
// discriminator PersonWorker writes for /person/{id}/tv_credits, so
// the UNIQUE (person_id, tmdb_credit_id) UPSERT branches harmonise.
//
// Title is sourced from tv.Name so the per-person filmography listing
// downstream (H-2 / cast.probeInLibrary) gets a usable label without
// a JOIN to canon.series. Year is left nil — the series_worker has
// no first_air_date in the patch shape at this seam; the PersonWorker
// fills it later from /person/{id}/tv_credits payload.
//
// Crew rows preserve Department / Job; cast rows preserve
// CharacterName + EpisodeCount. CreditOrder is mapped straight
// through from the TMDB aggregate_credits billing order (Story
// 1087b) so downstream credit-order sorts have a stable index.
func mapSeriesCreditsToPersonCredits(
	creds []people.SeriesCredit,
	tv *tmdb.TVResponse,
	tmdbMediaID int64,
	lastAppByPerson map[int64]int,
) []people.PersonCredit {
	title := ""
	if tv != nil {
		title = tv.Name
	}
	out := make([]people.PersonCredit, 0, len(creds))
	for _, cr := range creds {
		var lastApp *int
		if v, ok := lastAppByPerson[cr.PersonID]; ok && v > 0 {
			s := v
			lastApp = &s
		}
		out = append(out, people.PersonCredit{
			PersonID:             cr.PersonID,
			MediaType:            tmdb.MediaTypeTV,
			TMDBMediaID:          tmdbMediaID,
			TMDBCreditID:         cr.TMDBCreditID,
			Kind:                 cr.Kind,
			Title:                title,
			CharacterName:        cr.CharacterName,
			Department:           cr.Department,
			Job:                  cr.Job,
			EpisodeCount:         cr.EpisodeCount,
			CreditOrder:          cr.CreditOrder, // Story 1087b — aggregate_credits billing order.
			LastAppearanceSeason: lastApp,        // Story 1090 — max real season the person appears in.
		})
	}
	return out
}

// applyEpisodeCredits walks every fetched season's per-episode
// guest_stars + crew and projects them to []people.PersonCredit rows
// keyed by media_type='tv_episode', tmdb_media_id=<episode tmdb_id>.
// Returns the row slice + a drop counter for credits whose person
// stub upsert failed or whose tmdb_credit_id was empty.
//
// Person stubs: the series-level aggregate_credits payload does NOT
// include episode-only guest stars, so personIDByTMDB (built in step 6)
// is missing them. The helper upserts a People row for each unseen
// guest star / crew member before emitting the credit row. The upserted
// stubs reuse the existing PeopleRepo.Upsert path (idempotent on
// tmdb_id); a failed stub upsert is logged and the credit dropped.
//
// Mirrors the series-level mapSeriesCreditsToPersonCredits shape so the
// downstream PersonCreditsRepository.BatchUpsert UPSERT branches
// harmonise across both writers (UNIQUE (person_id, tmdb_credit_id) is
// globally unique per TMDB).
func (w *SeriesWorker) applyEpisodeCredits(
	txCtx context.Context,
	seasons map[int]*tmdb.SeasonResponse,
	personIDByTMDB map[int]int64,
	log *slog.Logger,
) ([]people.PersonCredit, int, error) {
	if len(seasons) == 0 {
		return nil, 0, nil
	}
	// Pre-size: most series episodes carry 1-5 guests + 2-4 crew.
	rows := make([]people.PersonCredit, 0, len(seasons)*8)
	dropped := 0
	for _, sr := range seasons {
		if sr == nil {
			continue
		}
		for _, ep := range sr.Episodes {
			if ep.ID == 0 {
				dropped += len(ep.GuestStars) + len(ep.Crew)
				continue
			}
			for _, g := range ep.GuestStars {
				if g.CreditID == "" {
					dropped++
					continue
				}
				pid, err := w.resolveOrUpsertEpisodePersonStub(txCtx, int(g.ID), g.Name, g.ProfilePath, personIDByTMDB, log)
				if err != nil || pid == 0 {
					dropped++
					continue
				}
				var characterPtr *string
				if g.Character != "" {
					ch := g.Character
					characterPtr = &ch
				}
				rows = append(rows, people.PersonCredit{
					PersonID:      pid,
					MediaType:     tmdb.MediaTypeTVEpisode,
					TMDBMediaID:   ep.ID,
					TMDBCreditID:  g.CreditID,
					Kind:          people.SeriesCreditCast,
					Title:         ep.Name,
					CharacterName: characterPtr,
				})
			}
			for _, c := range ep.Crew {
				if c.CreditID == "" {
					dropped++
					continue
				}
				pid, err := w.resolveOrUpsertEpisodePersonStub(txCtx, int(c.ID), c.Name, c.ProfilePath, personIDByTMDB, log)
				if err != nil || pid == 0 {
					dropped++
					continue
				}
				var deptPtr *string
				if c.Department != "" {
					d := c.Department
					deptPtr = &d
				}
				var jobPtr *string
				if c.Job != "" {
					j := c.Job
					jobPtr = &j
				}
				rows = append(rows, people.PersonCredit{
					PersonID:     pid,
					MediaType:    tmdb.MediaTypeTVEpisode,
					TMDBMediaID:  ep.ID,
					TMDBCreditID: c.CreditID,
					Kind:         people.SeriesCreditCrew,
					Title:        ep.Name,
					Department:   deptPtr,
					Job:          jobPtr,
				})
			}
		}
	}
	return rows, dropped, nil
}

// resolveOrUpsertEpisodePersonStub returns the canon person id for the
// given TMDB person id. If the id is already in personIDByTMDB (series
// aggregate_credits), the cached id is returned. Otherwise the helper
// upserts a stub Person and caches the id so the next episode with the
// same guest star reuses it without a second round-trip.
//
// Returns 0 + nil when tmdbPersonID is 0 (TMDB returned a bare row);
// caller treats that as a drop.
func (w *SeriesWorker) resolveOrUpsertEpisodePersonStub(
	txCtx context.Context,
	tmdbPersonID int,
	name string,
	profilePath string,
	personIDByTMDB map[int]int64,
	log *slog.Logger,
) (int64, error) {
	if tmdbPersonID == 0 {
		return 0, nil
	}
	if pid, ok := personIDByTMDB[tmdbPersonID]; ok && pid != 0 {
		return pid, nil
	}
	tid := domain.TMDBID(tmdbPersonID)
	stub := people.Person{
		Name:      name,
		Hydration: people.HydrationStub,
		TMDBID:    &tid,
	}
	if profilePath != "" {
		p := profilePath
		stub.ProfileAsset = &p
	}
	pid, err := w.deps.People.Upsert(txCtx, stub)
	if err != nil {
		log.WarnContext(txCtx, "enrichment.series.handle.episode_person_stub_failed",
			slog.Int("tmdb_person_id", tmdbPersonID),
			slog.String("name", name),
			slog.String("error", err.Error()),
		)
		return 0, err
	}
	personIDByTMDB[tmdbPersonID] = pid
	return pid, nil
}

// resolveSeriesCreditsWithPersonID re-walks tv.AggregateCredits and
// pairs every credit with its TMDB person id, then resolves the
// person id via personIDByTMDB. Order MUST match MapTVToCredits
// exactly — cast first (one row per cast member), then crew (one
// row per job per crew member). Returns the resolved slice + a
// drop count for any credit we cannot wire.
func resolveSeriesCreditsWithPersonID(tv *tmdb.TVResponse, seriesID domain.SeriesID, personIDByTMDB map[int]int64) ([]people.SeriesCredit, int) {
	if tv == nil || tv.AggregateCredits == nil {
		return nil, 0
	}
	out := make([]people.SeriesCredit, 0, len(tv.AggregateCredits.Cast)+len(tv.AggregateCredits.Crew))
	dropped := 0
	for _, cast := range tv.AggregateCredits.Cast {
		pid, ok := personIDByTMDB[int(cast.ID)]
		if !ok || pid == 0 {
			dropped++
			continue
		}
		creditID := ""
		var characterPtr *string
		if len(cast.Roles) > 0 {
			creditID = cast.Roles[0].CreditID
			if cast.Roles[0].Character != "" {
				ch := cast.Roles[0].Character
				characterPtr = &ch
			}
		}
		if creditID == "" {
			dropped++
			continue
		}
		order := cast.Order
		var episodeCountPtr *int
		if cast.TotalEpisodeCount > 0 {
			ec := cast.TotalEpisodeCount
			episodeCountPtr = &ec
		}
		out = append(out, people.SeriesCredit{
			SeriesID:      seriesID,
			PersonID:      pid,
			Kind:          people.SeriesCreditCast,
			TMDBCreditID:  creditID,
			CharacterName: characterPtr,
			CreditOrder:   &order,
			EpisodeCount:  episodeCountPtr,
		})
	}
	for _, crew := range tv.AggregateCredits.Crew {
		pid, ok := personIDByTMDB[int(crew.ID)]
		if !ok || pid == 0 {
			dropped += len(crew.Jobs)
			continue
		}
		var deptPtr *string
		if crew.Department != "" {
			d := crew.Department
			deptPtr = &d
		}
		for _, job := range crew.Jobs {
			if job.CreditID == "" {
				dropped++
				continue
			}
			var jobPtr *string
			if job.Job != "" {
				j := job.Job
				jobPtr = &j
			}
			var episodeCountPtr *int
			if job.EpisodeCount > 0 {
				ec := job.EpisodeCount
				episodeCountPtr = &ec
			}
			out = append(out, people.SeriesCredit{
				SeriesID:     seriesID,
				PersonID:     pid,
				Kind:         people.SeriesCreditCrew,
				TMDBCreditID: job.CreditID,
				Department:   deptPtr,
				Job:          jobPtr,
				EpisodeCount: episodeCountPtr,
			})
		}
	}
	return out, dropped
}

func (w *SeriesWorker) applyTaxonomyForLanguage(txCtx context.Context, seriesID domain.SeriesID, m mappedPayload, lang string) error {
	// B-26: Genres.Upsert touches the shared `genres` table (16 TMDB
	// TV genres, every series upserts 1-5 of them). Two parallel
	// series_worker txes acquire row-level UPDATE locks on overlapping
	// rows; without deterministic ordering Postgres reports
	// SQLSTATE 40P01 (2 series with cumulative 57 attempts in
	// 2026-06-22 audit). Sort in-place by tmdb_id ASC — genres are a
	// logical SET (no user-visible position), so the slight reorder
	// of series_genres.position (ListBySeries sorts by position ASC,
	// genre_id ASC anyway) is acceptable.
	slices.SortStableFunc(m.Genres, func(a, b taxonomy.Genre) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})

	gIDs := make([]int64, 0, len(m.Genres))
	for _, g := range m.Genres {
		id, err := w.deps.Genres.Upsert(txCtx, g)
		if err != nil {
			return fmt.Errorf("upsert genre: %w", err)
		}
		if g.Name != "" {
			// Story 533c: TMDB returns localised genre names per
			// ?language=X, so the i18n row is written keyed by the
			// current language. Composite PK (genre_id, language)
			// keeps the cross-language calls idempotent.
			if err := w.deps.Genres.UpsertI18n(txCtx, id, lang, g.Name); err != nil {
				return fmt.Errorf("upsert genres_i18n: %w", err)
			}
		}
		gIDs = append(gIDs, id)
	}
	if err := w.deps.Genres.Set(txCtx, seriesID, gIDs); err != nil {
		return fmt.Errorf("set series_genres: %w", err)
	}

	kIDs := make([]int64, 0, len(m.Keywords))
	for _, k := range m.Keywords {
		id, err := w.deps.Keywords.Upsert(txCtx, k)
		if err != nil {
			return fmt.Errorf("upsert keyword: %w", err)
		}
		if k.Name != "" {
			// Story 533c: TMDB returns localised keyword names per
			// ?language=X.
			if err := w.deps.Keywords.UpsertI18n(txCtx, id, lang, k.Name); err != nil {
				return fmt.Errorf("upsert keywords_i18n: %w", err)
			}
		}
		kIDs = append(kIDs, id)
	}
	if err := w.deps.Keywords.Set(txCtx, seriesID, kIDs); err != nil {
		return fmt.Errorf("set series_keywords: %w", err)
	}

	nIDs := make([]int64, 0, len(m.Networks))
	for _, n := range m.Networks {
		id, err := w.deps.Networks.Upsert(txCtx, n)
		if err != nil {
			return fmt.Errorf("upsert network: %w", err)
		}
		nIDs = append(nIDs, id)
	}
	if err := w.deps.Networks.Set(txCtx, seriesID, nIDs); err != nil {
		return fmt.Errorf("set series_networks: %w", err)
	}

	cIDs := make([]int64, 0, len(m.Companies))
	for _, c := range m.Companies {
		id, err := w.deps.Companies.Upsert(txCtx, c)
		if err != nil {
			return fmt.Errorf("upsert company: %w", err)
		}
		cIDs = append(cIDs, id)
	}
	if err := w.deps.Companies.Set(txCtx, seriesID, cIDs); err != nil {
		return fmt.Errorf("set series_companies: %w", err)
	}
	return nil
}

// ---- error handling + journal helpers ------------------------------

// terminalAttempts is the attempts sentinel for "no retries" failures
// (TMDB 404, no tmdb_id on canon). Mirrors the legacy
// sync_log.outcome=not_found semantics — the row exists so the
// dispatcher can surface degraded[], but ListDueForRetry won't pick it
// up because attempts > 5 trips the terminal-failure filter.
const terminalAttempts = 99

// omdbColdEnqueueTerminalAttempts is the W18-8 enqueue-time terminal-negative
// cutoff for OMDb. It mirrors the SQL `ee.attempts > 5` guard in
// SeriesRepository.ListLibraryWithIMDBStale (the daily batch selector) so a
// series that has exhausted OMDb retries is not passively re-enqueued on an
// imdb_id gain. Distinct from terminalAttempts (99, the not_found sentinel the
// OMDb worker writes) — this is the retry give-up threshold, not the sentinel.
const omdbColdEnqueueTerminalAttempts = 5

// handleTMDBError records an enrichment_errors row for a TMDB failure.
// TMDB 404 lands as attempts=terminalAttempts (no NextAttemptAt → no
// retry); other errors land as previousAttempts+1 with NextAttemptAt
// set via the existing backoff. Returns nil — the dispatcher only
// cares about success/failure for slog; the journalled row drives the
// retry sweep.
func (w *SeriesWorker) handleTMDBError(ctx context.Context, seriesID domain.SeriesID, op string, err error, previousAttempts int, start time.Time) error {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceTMDBSeries)),
		slog.String("op", op),
	)

	var apiErr *tmdb.APIError
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		w.recordEnrichmentError(ctx, seriesID, enrichment.SourceTMDBSeries, err, terminalAttempts, nil, log)
		log.InfoContext(ctx, "enrichment.series.handle.not_found",
			slog.Int("duration_ms", durMs),
		)
		return nil
	}

	// Retryable — record + backoff.
	attempts := previousAttempts + 1
	next := enrichment.NextAttemptAt(attempts, now)
	w.recordEnrichmentError(ctx, seriesID, enrichment.SourceTMDBSeries, err, attempts, &next, log)
	log.WarnContext(ctx, "enrichment.series.handle.failed",
		slog.Int("attempts", attempts),
		slog.Time("next_attempt_at", next),
		slog.Int("duration_ms", durMs),
		slog.String("error", err.Error()),
	)
	return nil
}

// recordEnrichmentError writes a single enrichment_errors row keyed by
// (series, source). The row is the durable failure ledger — the
// composer reads via EnrichmentFreshnessPort.ErrorsFor for degraded[]
// computation, the dispatcher reads via ListDueForRetry for retry
// scheduling. Failures are logged at WARN — a write miss is annoying
// but not fatal (the next worker attempt re-upserts the row).
func (w *SeriesWorker) recordEnrichmentError(
	ctx context.Context,
	seriesID domain.SeriesID,
	source enrichment.Source,
	cause error,
	attempts int,
	nextAttemptAt *time.Time,
	log *slog.Logger,
) {
	now := w.deps.Clock()
	rec := enrichment.EnrichmentError{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      int64(seriesID),
		Source:        source,
		LastError:     cause.Error(),
		Attempts:      attempts,
		LastSeenAt:    now,
		NextAttemptAt: nextAttemptAt,
	}
	if err := w.deps.EnrichmentErrors.RecordFailure(ctx, rec); err != nil {
		log.WarnContext(ctx, "enrichment.series.handle.record_failure_failed",
			slog.String("error", err.Error()))
	}
}

// journalOK stamps series.enrichment_tmdb_synced_at = now and clears
// any outstanding enrichment_errors row for (series, tmdb_series).
// Both writes fail silently with a WARN log — the next worker tick
// will retry, and the canon row's UPDATE is the source of truth for
// freshness (clear-on-success is best-effort).
func (w *SeriesWorker) journalOK(ctx context.Context, seriesID domain.SeriesID, now time.Time, durMs int) {
	if err := w.deps.Series.MarkTMDBSynced(ctx, seriesID, now); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.series.handle.mark_synced_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
	if err := w.deps.EnrichmentErrors.ClearOnSuccess(ctx,
		enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceTMDBSeries); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.series.handle.clear_error_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
	_ = durMs // surfaced on the caller's ok log line
}

// ---- mapping helpers (private) -------------------------------------

func patchFromTMDBCanon(c series.Canon) enrichment.SeriesPatch {
	return enrichment.SeriesPatch{
		TMDBID: tmdbIDPtrToInt(c.TMDBID), TVDBID: tvdbIDPtrToInt(c.TVDBID), IMDBID: imdbIDPtrToString(c.IMDBID),
		OriginalTitle: c.OriginalTitle, Status: c.Status,
		FirstAirDate: c.FirstAirDate, LastAirDate: c.LastAirDate,
		NextAirDate:      c.NextAirDate,
		Homepage:         c.Homepage,
		OriginalLanguage: c.OriginalLanguage,
		OriginCountry:    c.OriginCountry,
		OriginCountries:  append([]string(nil), c.OriginCountries...),
		Popularity:       c.Popularity,
		InProduction:     &c.InProduction,
		TMDBRating:       c.TMDBRating,
		TMDBVotes:        c.TMDBVotes,
		RuntimeMinutes:   c.RuntimeMinutes,
		// Title / PosterAsset / BackdropAsset dropped in S-E3a — canon no
		// longer carries them; series text/art flow through series_texts /
		// series_media_texts, not the merge-policy canon columns.
	}
}

func nonEmptyStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nonZeroFloatPtr returns nil for TMDB's "no rating" sentinel (0.0),
// else pointer to the value. Shared across narrow methods that write
// float64 rating/popularity fields (A3b RefreshRecommendations, future
// A5 orchestrator scope). Consolidates local A3b-suffixed helper per
// A3b Round-2 review LOW (Story 563 carry-forward).
func nonZeroFloatPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

// tvdbIDPtrToInt / intPtrToTVDBID bridge the typed TVDBID seam
// between series.Canon (*domain.TVDBID) and the domain/enrichment
// patch + canon shapes (*int). The enrichment package intentionally
// stays import-free of internal/shared/domain so the application
// layer does the cast at the merge seam.
func tvdbIDPtrToInt(p *domain.TVDBID) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func intPtrToTVDBID(p *int) *domain.TVDBID {
	if p == nil {
		return nil
	}
	v := domain.TVDBID(*p)
	return &v
}

// tmdbIDPtrToInt / intPtrToTMDBID bridge the typed TMDBID seam between
// series.Canon (*domain.TMDBID) and the domain/enrichment patch + canon
// shapes (*int). Same rationale as the TVDB bridge — domain/enrichment
// intentionally stays import-free of internal/shared/domain. Story 403
// A-5d-2.
func tmdbIDPtrToInt(p *domain.TMDBID) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func intPtrToTMDBID(p *int) *domain.TMDBID {
	if p == nil {
		return nil
	}
	v := domain.TMDBID(*p)
	return &v
}

// imdbIDPtrToString / stringPtrToIMDBID bridge the typed IMDBID seam
// between series.Canon (*domain.IMDBID) and the domain/enrichment
// patch + canon shapes (*string). Same rationale as the TVDB bridge:
// domain/enrichment intentionally stays import-free of
// internal/shared/domain so the application layer does the cast at
// the merge seam. Story 402 A-5d-1.
func imdbIDPtrToString(p *domain.IMDBID) *string {
	if p == nil {
		return nil
	}
	v := string(*p)
	return &v
}

func stringPtrToIMDBID(p *string) *domain.IMDBID {
	if p == nil {
		return nil
	}
	v := domain.IMDBID(*p)
	return &v
}

func canonToEnrichmentCanon(c series.Canon) enrichment.SeriesCanon {
	return enrichment.SeriesCanon{
		Hydration: enrichment.HydrationLevel(c.Hydration),
		TMDBID:    tmdbIDPtrToInt(c.TMDBID), TVDBID: tvdbIDPtrToInt(c.TVDBID), IMDBID: imdbIDPtrToString(c.IMDBID),
		OriginalTitle: c.OriginalTitle, Status: c.Status,
		FirstAirDate: c.FirstAirDate, LastAirDate: c.LastAirDate,
		NextAirDate: c.NextAirDate, Year: c.Year,
		RuntimeMinutes: c.RuntimeMinutes, Homepage: c.Homepage,
		OriginalLanguage: c.OriginalLanguage, OriginCountry: c.OriginCountry, OriginCountries: append([]string(nil), c.OriginCountries...),
		Popularity: c.Popularity, InProduction: c.InProduction,
		// Title / PosterAsset / BackdropAsset dropped in S-E3a (canon no
		// longer carries them). The enrichment-local mirror leaves them
		// zero; merge output for those fields is intentionally discarded.
		TMDBRating: c.TMDBRating, TMDBVotes: c.TMDBVotes,
		IMDBRating: c.IMDBRating, IMDBVotes: c.IMDBVotes,
		OMDBRated: c.OMDBRated, OMDBAwards: c.OMDBAwards,
	}
}

func enrichmentCanonToCanon(ec enrichment.SeriesCanon, base series.Canon) series.Canon {
	base.Hydration = series.Hydration(ec.Hydration)
	base.TMDBID = intPtrToTMDBID(ec.TMDBID)
	base.TVDBID = intPtrToTVDBID(ec.TVDBID)
	base.IMDBID = stringPtrToIMDBID(ec.IMDBID)
	// base.Title dropped in S-E3a — series display title is not a canon
	// field; series_texts is the source of truth.
	base.OriginalTitle = ec.OriginalTitle
	base.Status = ec.Status
	base.FirstAirDate = ec.FirstAirDate
	base.LastAirDate = ec.LastAirDate
	base.NextAirDate = ec.NextAirDate
	base.Year = ec.Year
	base.RuntimeMinutes = ec.RuntimeMinutes
	base.Homepage = ec.Homepage
	base.OriginalLanguage = ec.OriginalLanguage
	base.OriginCountry = ec.OriginCountry
	base.OriginCountries = append([]string(nil), ec.OriginCountries...)
	base.Popularity = ec.Popularity
	base.InProduction = ec.InProduction
	// base.PosterAsset / base.BackdropAsset dropped in S-E3a — series art
	// is read from series_media_texts, not canon columns.
	base.TMDBRating = ec.TMDBRating
	base.TMDBVotes = ec.TMDBVotes
	base.IMDBRating = ec.IMDBRating
	base.IMDBVotes = ec.IMDBVotes
	base.OMDBRated = ec.OMDBRated
	base.OMDBAwards = ec.OMDBAwards
	return base
}

func findEpisodeTitle(sr *tmdb.SeasonResponse, seasonNumber, episodeNumber int) string {
	if sr == nil {
		return ""
	}
	for _, e := range sr.Episodes {
		if e.SeasonNumber == seasonNumber && e.EpisodeNumber == episodeNumber {
			return e.Name
		}
	}
	return ""
}

func findEpisodeOverview(sr *tmdb.SeasonResponse, seasonNumber, episodeNumber int) string {
	if sr == nil {
		return ""
	}
	for _, e := range sr.Episodes {
		if e.SeasonNumber == seasonNumber && e.EpisodeNumber == episodeNumber {
			return e.Overview
		}
	}
	return ""
}

// compareTMDBID is the deterministic comparator used by the People /
// Genres / Recommendations upsert-loop sorts in series_worker. Sorts
// by *TMDBID ASCENDING, NULL entries pushed to the tail.
//
// Rationale — B-26: two parallel series_worker txes that both
// upsert overlapping people/genres/series rows acquire row-level
// UPDATE locks. If the lock-acquisition order differs between txes,
// Postgres detects a cycle and kills one (SQLSTATE 40P01). Sorting
// the per-tx loop by tmdb_id ASC enforces a global lock acquisition
// order — no cycle possible.
//
// NULL-tail rationale: NULL tmdb_id rows don't match the partial
// unique index `WHERE tmdb_id IS NOT NULL` and therefore don't take
// the contended row-lock — they go through INSERT. Placing them at
// the tail keeps non-NULL ordering correct without branching.
func compareTMDBID(a, b *domain.TMDBID) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil: // nil pushes to tail
		return 1
	case b == nil:
		return -1
	case *a < *b:
		return -1
	case *a > *b:
		return 1
	default:
		return 0
	}
}
