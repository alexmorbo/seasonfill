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
	SeriesMediaTexts  SeriesMediaTextsPort // S-E3a — per-lang poster/hero art (nil-OK)
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
	GetCanonicalSeason(ctx context.Context, seriesID domain.SeriesID, seasonNumber int, lang string) (SeasonDetail, bool, error)
	GetCanonicalCast(ctx context.Context, seriesID domain.SeriesID, lang string, limit int) ([]CastDetail, error)
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
	// Story 533 → Story 563 → B-recs-probe-lang follow-up — read-through
	// TMDB freshener scoped to SectionRecommendations. Prior shape probed
	// Skeleton+Overview+Cast+Media on the recs endpoint, which never
	// triggered the recommendation-lang coverage probe — the only site
	// that guards ru-RU rec titles on TMDB-only series. Fresh case
	// fast-paths without TMDB call; Stale/Cold dispatches Sync refresh.
	var freshen FreshenResult
	if u.d.Freshener != nil {
		freshen, _ = u.d.Freshener.EnsureFreshScope(ctx, seriesID, resolvedLang,
			[]freshener.Section{freshener.SectionRecommendations},
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

	// S-E3a — batch per-language poster paths for the resolved rec ids;
	// canon carries no poster_asset, so series_media_texts is the only
	// source. Failure degrades quietly to nil posters (FE monogram).
	var localisedMedia map[domain.SeriesID]series.SeriesMediaText
	if u.d.SeriesMediaTexts != nil && len(ids) > 0 {
		resolvedIDs := make([]domain.SeriesID, 0, len(ids))
		for _, recID := range ids {
			if _, ok := byID[recID]; ok {
				resolvedIDs = append(resolvedIDs, recID)
			}
		}
		if len(resolvedIDs) > 0 {
			var merr error
			localisedMedia, merr = u.d.SeriesMediaTexts.ListByIDsWithFallback(ctx, resolvedIDs, resolvedLang)
			if merr != nil {
				u.d.Logger.WarnContext(ctx, "tmdb_fallback_recommendations_media_failed",
					slog.Int64("series_id", int64(seriesID)),
					slog.String("lang", resolvedLang),
					slog.Int("rec_count", len(resolvedIDs)),
					slog.String("err", merr.Error()))
				localisedMedia = nil
			}
		}
	}

	resolved := make([]RecommendationDetail, 0, len(ids))
	for _, recID := range ids {
		s, ok := byID[recID]
		if !ok {
			continue
		}
		// S-E3a — display title staged from series_texts (lang → en-US),
		// else canon OriginalTitle. Canon carries no display title.
		rd := RecommendationDetail{Series: s, Title: recTitle(localised, recID, s)}
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
				// S-E3a — poster raw path from series_media_texts only.
				var raw *string
				if localisedMedia != nil {
					if mt, ok := localisedMedia[out.Items[i].Series.ID]; ok && mt.PosterAsset != nil && *mt.PosterAsset != "" {
						raw = mt.PosterAsset
					}
				}
				out.Items[i].PosterAsset = u.d.MediaResolver.Resolve(ctx, raw, "w342", "poster_w342")
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
	// project SeriesSummary (status/year range) without a second port
	// lookup. Same posture as GetCanonical's *Detail.Canon.
	Canon series.Canon
	// S-E3a — hero Title + PosterAsset staged from series_texts /
	// series_media_texts (lang → en-US; Title falls back to
	// canon.OriginalTitle). PosterAsset is already the resolved media hash.
	Title       string
	PosterAsset *string
	Cast        []CastDetail
	Degraded    []string
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
	// S-E3a — hero title from series_texts (lang → en-US), else canon
	// OriginalTitle. Canon carries no display title.
	heroTitle := ""
	if u.d.SeriesTexts != nil {
		if t, terr := u.d.SeriesTexts.GetWithFallback(ctx, seriesID, resolvedLang); terr == nil && t.Title != nil && *t.Title != "" {
			heroTitle = *t.Title
		}
	}
	if heroTitle == "" && canon.OriginalTitle != nil {
		heroTitle = *canon.OriginalTitle
	}
	// S-E3a — hero poster raw path from series_media_texts (lang → en-US);
	// canon carries no poster_asset. Story 545 (Bug #3) — resolve to the
	// content-addressed media hash so the wire never emits a raw TMDB path.
	var posterRaw *string
	if u.d.SeriesMediaTexts != nil {
		if mt, merr := u.d.SeriesMediaTexts.GetWithFallback(ctx, seriesID, resolvedLang); merr == nil && mt.PosterAsset != nil && *mt.PosterAsset != "" {
			p := *mt.PosterAsset
			posterRaw = &p
		}
	}
	if u.d.MediaResolver != nil {
		syncCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		posterRaw = u.d.MediaResolver.ResolveSync(syncCtx, posterRaw, "w342", "poster_w342")
		cancel()
	}
	out := &CastFallbackResult{
		SeriesID:    seriesID,
		Lang:        resolvedLang,
		Canon:       canon,
		Title:       heroTitle,
		PosterAsset: posterRaw,
		Cast:        []CastDetail{},
		Degraded:    []string{},
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
		if cast, cerr := u.d.SeasonsCastSource.GetCanonicalCast(ctx, seriesID, resolvedLang, limit); cerr == nil {
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

// GetSeason — canon-only single-season detail for a TMDB-only series.
// Mirrors GetCanonicalCast posture: freshener scoped to THIS season
// (the user is waiting on exactly this season, ModeSync, singleflight),
// synchronous canon load via SeriesPort (ports.ErrNotFound bubbles up so
// the handler dispatches 404 series_not_found), then a best-effort
// SeasonsCastSource read of the single canon season.
//
// The returned *Detail carries Instance="" + SonarrSeriesID=0 and no
// per-instance episode state (all EpisodeDetail.State=nil → no on-disk
// badges), so the handler projects the SAME dto.SeasonDetailResponse
// shape via mapSeasons that the per-instance path produces — but with
// degraded carrying "tmdb_series". Detail.Seasons is empty (non-nil)
// when the series has no such season; the handler maps that to 404
// season_not_found.
func (u *TMDBFallbackUseCase) GetSeason(ctx context.Context, seriesID domain.SeriesID, seasonNumber int, lang string) (*Detail, error) {
	resolvedLang := resolveLang(lang)
	var freshen FreshenResult
	if u.d.Freshener != nil {
		freshen, _ = u.d.Freshener.EnsureFreshScope(ctx, seriesID, resolvedLang,
			[]freshener.Section{freshener.SeasonSection(seasonNumber)},
			nil, false, ModeSync,
		)
	}
	canon, err := u.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("tmdbfallback: canon load: %w", err)
	}
	d := &Detail{
		Instance:       "",
		SonarrSeriesID: 0,
		SeriesID:       seriesID,
		Lang:           resolvedLang,
		Canon:          canon,
		Seasons:        []SeasonDetail{},
		Degraded:       []enrichment.Source{},
		SyncedAt:       u.d.Now(),
	}
	mark := func() {
		for _, s := range d.Degraded {
			if s == enrichment.SourceTMDBSeries {
				return
			}
		}
		d.Degraded = append(d.Degraded, enrichment.SourceTMDBSeries)
	}
	if canon.Hydration != series.HydrationFull {
		mark()
	}
	if freshen.Degraded {
		mark()
	}
	if u.d.SeasonsCastSource != nil {
		if sd, ok, serr := u.d.SeasonsCastSource.GetCanonicalSeason(ctx, seriesID, seasonNumber, resolvedLang); serr != nil {
			u.d.Logger.WarnContext(ctx, "tmdb_fallback_season_load_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.Int("season_number", seasonNumber),
				slog.String("err", serr.Error()))
			mark()
		} else if ok {
			d.Seasons = []SeasonDetail{sd}
		}
	}
	if u.d.Enricher != nil && canon.Hydration != series.HydrationFull {
		u.d.Enricher.EnqueueIfStale(seriesID, canon.Hydration)
	}
	u.d.Logger.InfoContext(ctx, "tmdb_fallback_season_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.Int("season_number", seasonNumber),
		slog.String("hydration", string(canon.Hydration)),
		slog.String("lang", resolvedLang),
		slog.Int("season_found", len(d.Seasons)),
		slog.Int("degraded_count", len(d.Degraded)))
	return d, nil
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
