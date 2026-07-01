// Package seriesdetail — see ports.go header.
//
// tmdb_fallback_usecase.go (Story 491 / N-1a + Story 532). Canon-only
// views for series not present in any Sonarr library. Discovery (N-2)
// lazy-stub-upserts canon rows from TMDB ids — this UC trusts that the
// canon row exists. Story 491 added GetCanonical (whole Detail). Story
// 532 adds GetOverview + GetRecommendations canon-only siblings so the
// per-section endpoints (Stories 529 / 530) don't 404 on TMDB-only
// series. Story 533b tightened the "tmdb_series" degraded marker:
// appended only when canon is stub-hydration, freshener degraded, or a
// real port load failed — NOT unconditionally for every fallback view.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// TMDBFallbackDeps — narrow ports. Existing fields keep nil-fallback
// semantics. Story 532 fields (SeriesTexts, Keywords, Recommendations,
// SeriesCacheLookup) are nil-OK — when nil, the corresponding branch
// silently degrades to empty.
type TMDBFallbackDeps struct {
	Series        SeriesPort
	MediaResolver *media.Resolver
	// Enricher is the Story 528 nil-OK lazy on-demand trigger. When
	// non-nil and the resolved canon row is stub-hydration, the use
	// case fires a fire-and-forget enrichment enqueue so a subsequent
	// SPA re-poll receives the hydrated row. nil keeps the UC working
	// unchanged when the enrichment subsystem is disabled at boot.
	Enricher OnDemandEnricher
	Logger   *slog.Logger
	Now      func() time.Time

	// Story 532 — canon-keyed nil-OK ports for the new section methods.
	SeriesTexts       SeriesTextsPort
	Keywords          KeywordsPort
	Recommendations   RecommendationsPort
	SeriesCacheLookup SeriesCacheLookupPort

	// Freshener (Story 533) — synchronous read-through TMDB refresh on
	// cold/stale detail open. nil-OK: when nil, the UC behaves exactly
	// like Story 532 (canon-only read, async enqueue via Enricher).
	// When non-nil, EnsureFresh runs BEFORE the canon load so a stub or
	// stale row gets lifted to full in the SAME request (3s budget).
	Freshener SeriesFreshener

	// SeasonsCastSource (Story 533a) — when non-nil, GetCanonical
	// populates Detail.Seasons + Detail.Cast from local DB. nil-OK:
	// when nil, Seasons + Cast stay empty (Story 532 behaviour). The
	// caller passes the same Composer instance the per-instance path
	// uses — repos are shared, no double DB connection.
	SeasonsCastSource CanonicalSeasonsCastReader
}

// CanonicalSeasonsCastReader is the seam Composer satisfies for the
// fallback's seasons+cast extension (Story 533a). Composer's
// GetCanonicalSeasons + GetCanonicalCast methods match this contract.
type CanonicalSeasonsCastReader interface {
	GetCanonicalSeasons(ctx context.Context, seriesID domain.SeriesID, lang string) ([]SeasonDetail, error)
	GetCanonicalCast(ctx context.Context, seriesID domain.SeriesID, limit int) ([]CastDetail, error)
}

// TMDBFallbackUseCase returns canon-only views.
type TMDBFallbackUseCase struct {
	d TMDBFallbackDeps
}

// NewTMDBFallbackUseCase constructs the use case.
func NewTMDBFallbackUseCase(d TMDBFallbackDeps) (*TMDBFallbackUseCase, error) {
	if d.Series == nil {
		return nil, errors.New("tmdbfallback: Series required")
	}
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MediaResolver == nil {
		d.MediaResolver = media.NewNopResolver()
	}
	return &TMDBFallbackUseCase{d: d}, nil
}

// GetCanonical projects a canon series row into a minimal Detail.
// Returns the upstream error (e.g. ports.ErrNotFound wrapped) when no
// canon row exists.
func (u *TMDBFallbackUseCase) GetCanonical(ctx context.Context, seriesID domain.SeriesID, lang string) (*Detail, error) {
	resolvedLang := resolveLang(lang)
	var freshen FreshenResult
	if u.d.Freshener != nil {
		freshen, _ = u.d.Freshener.EnsureFreshScope(ctx, seriesID, resolvedLang,
			[]freshener.Section{
				freshener.SectionSkeleton,
				freshener.SectionOverview,
				freshener.SectionCast,
				freshener.SectionMedia,
			},
			nil, false, ModeSync,
		)
	}
	canon, err := u.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("tmdbfallback: canon load: %w", err)
	}
	lang = resolvedLang
	d := &Detail{
		SeriesID:           seriesID,
		Lang:               lang,
		Canon:              canon,
		ExternalIDs:        map[string]string{},
		InLibraryInstances: []domain.InstanceName{},
		Torrents:           TorrentsPlaceholder{SyncPending: false},
		SyncedAt:           u.d.Now(),
	}
	// Degraded: if canon row is stub (hydration != full), tmdb_series is
	// degraded — the FE shows a "loading info" placeholder until N-2 / the
	// enrichment worker fills the canon row.
	if canon.Hydration != series.HydrationFull {
		d.Degraded = []enrichment.Source{enrichment.SourceTMDBSeries}
		// Story 528 — lazy on-demand enrichment trigger. Fires only for
		// stub canon rows; the call is synchronous + non-blocking by
		// contract (adapter goroutines the actual dispatcher Enqueue).
		// nil-safe — UC continues to return canon-only Detail when
		// enrichment is disabled at boot.
		if u.d.Enricher != nil {
			u.d.Enricher.EnqueueIfStale(seriesID, canon.Hydration)
		}
	}
	// Media resolution: best-effort hero hash translation (same pattern as
	// Composer.resolveAssets but synchronous-only — no recommendation /
	// season walks since those slices are empty).
	if u.d.MediaResolver != nil {
		syncCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		d.Canon.PosterAsset = u.d.MediaResolver.ResolveSync(syncCtx, d.Canon.PosterAsset, "w342", "poster_w342")
		d.Canon.BackdropAsset = u.d.MediaResolver.ResolveSync(syncCtx, d.Canon.BackdropAsset, "w1280", "backdrop_w1280")
	}
	// Story 533 — append tmdb_series to degraded[] when the sync refresh
	// fell back to async. The existing stub-branch already adds it; this
	// branch covers the case where canon was non-stub but refresh hit the
	// network and timed out (e.g. TTL-stale row + TMDB down).
	if freshen.Degraded && !contains(d.Degraded, enrichment.SourceTMDBSeries) {
		d.Degraded = append(d.Degraded, enrichment.SourceTMDBSeries)
	}
	// Story 533d — populate Detail.Text from series_texts so the DTO
	// mapHero override picks up the localized title/tagline (same
	// contract as Composer.Get branch "a"). nil-OK: when SeriesTexts is
	// not wired (tests), the fallback path keeps canon.title as before.
	// ErrNotFound is a soft miss (no row at all — cold series); any
	// other port error logs + appends tmdb_series to Degraded.
	// Story 541 — when GetWithFallback returns a row in a language other
	// than the request AND canon.OriginalLanguage matches the request,
	// drop d.Text so mapHero renders canon.Title (the original-language
	// title) instead of the en-US fallback row.
	if u.d.SeriesTexts != nil {
		t, terr := u.d.SeriesTexts.GetWithFallback(ctx, seriesID, lang)
		switch {
		case terr == nil:
			if !shouldPreferCanon(canon, lang, t.Language) {
				tt := t
				d.Text = &tt
			}
		case errors.Is(terr, ports.ErrNotFound):
			// cold series — leave d.Text nil, mapHero falls back to canon
		default:
			u.d.Logger.WarnContext(ctx, "tmdb_fallback_series_texts_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("err", terr.Error()))
			if !contains(d.Degraded, enrichment.SourceTMDBSeries) {
				d.Degraded = append(d.Degraded, enrichment.SourceTMDBSeries)
			}
		}
	}
	// Story 533a — populate seasons + cast from local DB when wired.
	// Failure is logged and degraded[] gains tmdb_series; never 5xx.
	if u.d.SeasonsCastSource != nil {
		if seasons, serr := u.d.SeasonsCastSource.GetCanonicalSeasons(ctx, seriesID, lang); serr == nil {
			d.Seasons = seasons
		} else {
			u.d.Logger.WarnContext(ctx, "tmdb_fallback_canon_seasons_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("err", serr.Error()))
			if !contains(d.Degraded, enrichment.SourceTMDBSeries) {
				d.Degraded = append(d.Degraded, enrichment.SourceTMDBSeries)
			}
		}
		if cast, cerr := u.d.SeasonsCastSource.GetCanonicalCast(ctx, seriesID, 0); cerr == nil {
			d.Cast = cast
		} else {
			u.d.Logger.WarnContext(ctx, "tmdb_fallback_canon_cast_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("err", cerr.Error()))
			if !contains(d.Degraded, enrichment.SourceTMDBSeries) {
				d.Degraded = append(d.Degraded, enrichment.SourceTMDBSeries)
			}
		}
	}
	u.d.Logger.InfoContext(ctx, "tmdb_fallback_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("hydration", string(canon.Hydration)),
		slog.String("lang", lang),
		slog.Int("season_count", len(d.Seasons)),
		slog.Int("cast_count", len(d.Cast)),
		slog.Bool("has_text", d.Text != nil),
	)
	return d, nil
}

// GetOverview — canon-only overview for TMDB-only series. Returns the
// upstream error (e.g. ports.ErrNotFound wrapped) when the canon row is
// absent. Story 532.
func (u *TMDBFallbackUseCase) GetOverview(ctx context.Context, seriesID domain.SeriesID, lang string) (*Overview, error) {
	resolvedLang := resolveLang(lang)
	var freshen FreshenResult
	if u.d.Freshener != nil {
		freshen, _ = u.d.Freshener.EnsureFreshScope(ctx, seriesID, resolvedLang,
			[]freshener.Section{
				freshener.SectionSkeleton,
				freshener.SectionOverview,
				freshener.SectionCast,
				freshener.SectionMedia,
			},
			nil, false, ModeSync,
		)
	}
	canon, err := u.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("tmdbfallback: canon load: %w", err)
	}
	lang = resolvedLang
	out := &Overview{
		Instance:       "",
		SonarrSeriesID: 0,
		SeriesID:       seriesID,
		Lang:           lang,
		Degraded:       []string{},
	}
	mark := func() {
		if !containsString(out.Degraded, "tmdb_series") {
			out.Degraded = append(out.Degraded, "tmdb_series")
		}
	}
	if canon.Hydration != series.HydrationFull {
		mark()
	}
	if freshen.Degraded {
		mark()
	}
	if u.d.SeriesTexts != nil {
		if t, terr := u.d.SeriesTexts.GetWithFallback(ctx, seriesID, lang); terr == nil {
			// Story 541 — same canon-preference guard as GetCanonical:
			// skip the row when it's the en-US fallback AND canon's
			// original language matches the request. Overview language
			// stays unset → DTO falls back to canon-derived description.
			if !shouldPreferCanon(canon, lang, t.Language) {
				if t.Overview != nil {
					out.Description = *t.Overview
				}
				out.DescriptionLanguage = t.Language
			}
		} else if !errors.Is(terr, ports.ErrNotFound) {
			u.d.Logger.WarnContext(ctx, "tmdb_fallback_overview_texts_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("err", terr.Error()))
			mark()
		}
	}
	if u.d.Keywords != nil {
		if kwIDs, kerr := u.d.Keywords.ListBySeries(ctx, seriesID); kerr == nil {
			if len(kwIDs) > 0 {
				// Story 552 (E-1 Z3) — batch i18n fetch. Failure degrades
				// quietly (mark + drop) like the original loop's missing
				// keyword skip.
				kws, lerr := u.d.Keywords.ListByIDsWithFallback(ctx, kwIDs, lang)
				if lerr != nil {
					u.d.Logger.WarnContext(ctx, "tmdb_fallback_overview_keywords_failed",
						slog.Int64("series_id", int64(seriesID)),
						slog.String("err", lerr.Error()))
					mark()
				} else {
					byID := make(map[int64]taxonomy.Keyword, len(kws))
					for _, k := range kws {
						byID[k.ID] = k
					}
					for _, id := range kwIDs {
						if k, ok := byID[id]; ok {
							out.Keywords = append(out.Keywords, k)
						}
					}
				}
			}
		} else {
			u.d.Logger.WarnContext(ctx, "tmdb_fallback_overview_keywords_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("err", kerr.Error()))
			mark()
		}
	}
	if canon.OMDBAwards != nil && *canon.OMDBAwards != "" && *canon.OMDBAwards != "N/A" {
		v := *canon.OMDBAwards
		out.Awards = &v
	}
	if u.d.Enricher != nil && canon.Hydration != series.HydrationFull {
		u.d.Enricher.EnqueueIfStale(seriesID, canon.Hydration)
	}
	u.d.Logger.InfoContext(ctx, "tmdb_fallback_overview_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("hydration", string(canon.Hydration)),
		slog.String("lang", lang),
		slog.Int("keyword_count", len(out.Keywords)),
		slog.Bool("has_awards", out.Awards != nil),
		slog.Bool("has_description", out.Description != ""),
		slog.Int("degraded_count", len(out.Degraded)))
	return out, nil
}

// GetRecommendations — canon-only recommendations for TMDB-only series.
// limit/offset are clamped per Composer.GetRecommendations defaults.
// Returns the upstream error (e.g. ports.ErrNotFound wrapped) when the
// canon row for the source series is absent. Story 532.
//
// lang (Story 565 B-recs-lang) — BCP-47 tag; passed to freshener for
// the missing_lang trip (matches Composer.GetCanonical semantics) and
// used to batch-load localised series_texts for each rec so
// canon.Title is overridden with the ru-RU (or configured) title.
// Empty / invalid values normalise to en-US via resolveLang.
func (u *TMDBFallbackUseCase) GetRecommendations(ctx context.Context, seriesID domain.SeriesID, lang string, limit, offset int) (*Recommendations, error) {
	if limit <= 0 {
		limit = RecommendationsLimitDefault
	}
	if limit > RecommendationsLimitMax {
		limit = RecommendationsLimitMax
	}
	if offset < 0 {
		offset = 0
	}
	resolvedLang := resolveLang(lang)
	// Story 533 → Story 563 — read-through TMDB freshener. Story 565
	// upgrades the freshener probe language from the previous "en-US
	// hard-coded" to the request lang so a missing-ru-RU on a
	// TMDB-only series can trip the missing_lang refresh instead of
	// silently returning stale en-US titles.
	var freshen FreshenResult
	if u.d.Freshener != nil {
		freshen, _ = u.d.Freshener.EnsureFreshScope(ctx, seriesID, resolvedLang,
			[]freshener.Section{
				freshener.SectionSkeleton,
				freshener.SectionOverview,
				freshener.SectionCast,
				freshener.SectionMedia,
			},
			nil, false, ModeSync,
		)
	}
	canon, err := u.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("tmdbfallback: canon load: %w", err)
	}
	out := &Recommendations{
		Instance:       "",
		SonarrSeriesID: 0,
		SeriesID:       seriesID,
		Items:          []RecommendationDetail{},
		Degraded:       []string{},
	}
	mark := func() {
		if !containsString(out.Degraded, "tmdb_series") {
			out.Degraded = append(out.Degraded, "tmdb_series")
		}
	}
	if canon.Hydration != series.HydrationFull {
		mark()
	}
	if freshen.Degraded {
		mark()
	}
	if u.d.Recommendations == nil {
		return out, nil
	}
	ids, err := u.d.Recommendations.ListBySeries(ctx, seriesID)
	if err != nil {
		u.d.Logger.WarnContext(ctx, "tmdb_fallback_recommendations_list_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("err", err.Error()))
		mark()
		return out, nil
	}
	// Story 551 (E-1 Z2) — batch the canon stub hydration, mirroring the
	// composer.go loadRecommendations path. The previous shape issued one
	// series SELECT per recommendation (M=10-20 per detail open). One
	// `id IN (?)` query now resolves the whole set; id-order preservation
	// is enforced by iterating the original `ids` slice when projecting
	// into RecommendationDetail rows. Stubs absent from the map mirror the
	// prior ErrNotFound continue branch (silently skipped).
	canons, lerr := u.d.Series.ListByIDs(ctx, ids)
	if lerr != nil {
		u.d.Logger.WarnContext(ctx, "tmdb_fallback_recommendations_batch_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("err", lerr.Error()))
		mark()
		return out, nil
	}
	byID := make(map[domain.SeriesID]series.Canon, len(canons))
	for _, canon := range canons {
		byID[canon.ID] = canon
	}

	// Story 565 (B-recs-lang) — batch-load localised titles. Failure
	// degrades quietly to canon titles + warn log; missing entries are
	// the norm for cold recs (freshener hasn't populated ru-RU yet).
	var localised map[domain.SeriesID]series.SeriesText
	if u.d.SeriesTexts != nil && len(ids) > 0 {
		resolvedIDs := make([]domain.SeriesID, 0, len(ids))
		for _, recID := range ids {
			if _, ok := byID[recID]; ok {
				resolvedIDs = append(resolvedIDs, recID)
			}
		}
		if len(resolvedIDs) > 0 {
			var terr error
			localised, terr = u.d.SeriesTexts.ListByIDsWithFallback(ctx, resolvedIDs, resolvedLang)
			if terr != nil {
				u.d.Logger.WarnContext(ctx, "tmdb_fallback_recommendations_texts_failed",
					slog.Int64("series_id", int64(seriesID)),
					slog.String("lang", resolvedLang),
					slog.Int("rec_count", len(resolvedIDs)),
					slog.String("err", terr.Error()))
				localised = nil
			}
		}
	}

	resolved := make([]RecommendationDetail, 0, len(ids))
	for _, recID := range ids {
		s, ok := byID[recID]
		if !ok {
			continue
		}
		// Story 565 — override canon.Title with the localised row when
		// present. Same guard as Composer.GetRecommendations.
		if localised != nil {
			if t, has := localised[recID]; has && t.Title != nil && *t.Title != "" {
				s.Title = *t.Title
			}
		}
		rd := RecommendationDetail{Series: s}
		if u.d.SeriesCacheLookup != nil {
			caches, _ := u.d.SeriesCacheLookup.ListBySeriesID(ctx, recID)
			if len(caches) > 0 {
				rd.InLibrary = true
				rd.InstanceName = caches[0].InstanceName
				rd.SonarrSeriesID = caches[0].SonarrSeriesID
			}
		}
		resolved = append(resolved, rd)
	}
	out.TotalCount = len(resolved)
	if offset >= len(resolved) {
		out.Items = []RecommendationDetail{}
		out.HasMore = false
	} else {
		end := min(offset+limit, len(resolved))
		out.Items = resolved[offset:end]
		out.HasMore = end < len(resolved)
		if u.d.MediaResolver != nil {
			for i := range out.Items {
				out.Items[i].Series.PosterAsset = u.d.MediaResolver.Resolve(ctx, out.Items[i].Series.PosterAsset, "w342", "poster_w342")
			}
		}
	}
	u.d.Logger.InfoContext(ctx, "tmdb_fallback_recommendations_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("hydration", string(canon.Hydration)),
		slog.String("lang", resolvedLang),
		slog.Int("limit", limit),
		slog.Int("offset", offset),
		slog.Int("total_count", out.TotalCount),
		slog.Int("items_returned", len(out.Items)),
		slog.Bool("has_more", out.HasMore),
		slog.Int("degraded_count", len(out.Degraded)))
	return out, nil
}

// CastFallbackResult — canon-only cast for a TMDB-only series. The
// handler projects this onto dto.SeriesCastResponse via the canon
// row (Title/PosterAsset/Status/Year range) — there's no TotalEpisodeCount
// or InLibrary probe on the TMDB-only path. Story 535 (B-42c).
type CastFallbackResult struct {
	SeriesID domain.SeriesID
	Lang     string
	// Canon is the resolved series.Canon row — exposed so the handler can
	// project SeriesSummary without a second port lookup. Same posture as
	// GetCanonical's *Detail.Canon.
	Canon    series.Canon
	Cast     []CastDetail
	Degraded []string
}

// GetCanonicalCast — canon-only cast list for TMDB-only series. limit
// clamps to CastDefaultLimit when <= 0. Story 535 (B-42c). Mirrors
// GetOverview / GetRecommendations posture: synchronous canon load via
// SeriesPort (ports.ErrNotFound bubbles up so the handler can dispatch
// 404 series_not_found); best-effort SeasonsCastSource read with
// degraded marker on failure.
func (u *TMDBFallbackUseCase) GetCanonicalCast(ctx context.Context, seriesID domain.SeriesID, lang string, limit int) (*CastFallbackResult, error) {
	if limit <= 0 {
		limit = CastDefaultLimit
	}
	resolvedLang := resolveLang(lang)
	var freshen FreshenResult
	if u.d.Freshener != nil {
		freshen, _ = u.d.Freshener.EnsureFreshScope(ctx, seriesID, resolvedLang,
			[]freshener.Section{
				freshener.SectionSkeleton,
				freshener.SectionOverview,
				freshener.SectionCast,
				freshener.SectionMedia,
			},
			nil, false, ModeSync,
		)
	}
	canon, err := u.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("tmdbfallback: canon load: %w", err)
	}
	// Story 545 (Bug #3) — hero poster on the cast page must use the
	// content-addressed media proxy hash, not the raw TMDB path. Mirrors
	// the GetCanonical path (line 139) so the TMDB-only cast page and
	// the TMDB-only detail page share the same hero-resolution surface.
	// Without this the wire emits `/abc.jpg` and the FE renders
	// `/api/v1/media/%2Fabc.jpg` → 404.
	if u.d.MediaResolver != nil {
		syncCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		canon.PosterAsset = u.d.MediaResolver.ResolveSync(syncCtx, canon.PosterAsset, "w342", "poster_w342")
		cancel()
	}
	out := &CastFallbackResult{
		SeriesID: seriesID,
		Lang:     resolvedLang,
		Canon:    canon,
		Cast:     []CastDetail{},
		Degraded: []string{},
	}
	mark := func() {
		if !containsString(out.Degraded, "tmdb_series") {
			out.Degraded = append(out.Degraded, "tmdb_series")
		}
	}
	if canon.Hydration != series.HydrationFull {
		mark()
	}
	if freshen.Degraded {
		mark()
	}
	if u.d.SeasonsCastSource != nil {
		if cast, cerr := u.d.SeasonsCastSource.GetCanonicalCast(ctx, seriesID, limit); cerr == nil {
			out.Cast = cast
		} else {
			u.d.Logger.WarnContext(ctx, "tmdb_fallback_cast_load_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("err", cerr.Error()))
			mark()
		}
	}
	if u.d.Enricher != nil && canon.Hydration != series.HydrationFull {
		u.d.Enricher.EnqueueIfStale(seriesID, canon.Hydration)
	}
	u.d.Logger.InfoContext(ctx, "tmdb_fallback_cast_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("hydration", string(canon.Hydration)),
		slog.String("lang", resolvedLang),
		slog.Int("cast_count", len(out.Cast)),
		slog.Int("degraded_count", len(out.Degraded)))
	return out, nil
}

// containsString is a []string variant of contains[T comparable]. The fallback
// overview / recs paths dedupe on the string-DTO marker ("tmdb_series") rather
// than enrichment.Source, so contains[enrichment.Source] doesn't fit.
func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
