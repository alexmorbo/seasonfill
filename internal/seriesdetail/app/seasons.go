// Package seriesdetail — E-1 B3c SeasonsComposer (PLAN §7.3, lines 2132-2251).
//
// SeasonsComposer builds the list-of-seasons document read by the SPA's
// accordion (posters + counts + localized names, NO episodes embed). Sibling to
// skeleton.go / cast.go — single-purpose read, never the fat 9-branch errgroup.
// Seasons are canon-level data (no per-instance Sonarr state), so this composer
// needs neither series_cache resolution nor a Sonarr client — just series (404
// gate + SyncedAt), seasons, season_texts (localized names), an episode aggregate
// (air_date_end MAX + episode_count), and the shared MediaResolver for per-season
// poster hashes.
package seriesdetail

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SeasonsListDTO is the SeasonsComposer return document (PLAN §7.3). SeriesID is
// the typed VO — a bare int fails the CI guard TestBareIDIntRegression (H-2).
type SeasonsListDTO struct {
	SeriesID domain.SeriesID `json:"series_id"`
	Seasons  []SeasonSummary `json:"seasons"`
	// ServedLanguage is the BCP-47 language the season names were principally
	// served in (W15-9): the requested lang when every resolved name is in it
	// (or there are no name rows), else the first-seen fallback language. Empty
	// when no season carried a localized name. The handler appends the
	// "missing_lang" marker when it differs from the request.
	ServedLanguage string    `json:"served_language,omitempty"`
	Degraded       []string  `json:"degraded,omitempty"`
	SyncedAt       time.Time `json:"synced_at"`
}

// SeasonSummary is one accordion row. No episodes embed (that's /season/:n); no
// per-instance state (that's /library?instance=). PosterAsset is a sha256 hash
// served via /api/v1/media/:hash, nil when TMDB provided no season poster (or it
// is not yet downloaded).
type SeasonSummary struct {
	SeasonNumber int        `json:"season_number"`
	Name         string     `json:"name"`
	AirDateStart *time.Time `json:"air_date_start,omitempty"`
	AirDateEnd   *time.Time `json:"air_date_end,omitempty"`
	EpisodeCount int        `json:"episode_count"`
	PosterAsset  *string    `json:"poster_asset,omitempty"`
	Overview     string     `json:"overview,omitempty"`
}

// SeasonsDeps groups the composer's narrow ports. Freshener + MediaResolver are
// nil-OK seams (defaulted in NewSeasonsComposer).
type SeasonsDeps struct {
	Series           SeriesPort
	Seasons          SeasonsPort
	SeasonTexts      SeasonTextsPort
	SeasonMediaTexts SeasonMediaTextsPort // S-C2 — nil-OK, canon poster fallback
	Aggregates       SeasonEpisodeAggregatesPort
	Freshener        SeriesFreshener
	MediaResolver    *media.Resolver
	Logger           *slog.Logger
	Now              func() time.Time
}

// SeasonsComposer is the one application use case for the /series/:id/seasons page.
type SeasonsComposer struct {
	d SeasonsDeps
}

// NewSeasonsComposer applies the package defaults (Logger, Now, nop resolver),
// identical to NewSkeletonComposer / NewCastComposer.
func NewSeasonsComposer(d SeasonsDeps) *SeasonsComposer {
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MediaResolver == nil {
		d.MediaResolver = media.NewNopResolver()
	}
	return &SeasonsComposer{d: d}
}

// Compose reads the season list for a canonical series.id. lang is a BCP-47 tag
// passed VERBATIM to the freshener + season_texts repo (operator directive §4.1,
// no server-side normalization; the repo normalises "" → en-US internally).
//
// Steps (PLAN §7.3 line 2170):
//  1. EnsureFreshScope(SectionSkeleton) — SAME scope as the skeleton read so the
//     probe does not fire a second time for the same open. nil-OK.
//  2. Series.Get — 404 gate (typed ErrNotFound propagates) + SyncedAt source.
//  3. Seasons.ListBySeries (canon rows, season_number ASC).
//  4. SeasonTexts.ListBySeriesWithFallback (ru-RU→en-US; canon name = tier 3).
//  5. Aggregates.AggregateBySeries (episode_count + air_date_end = MAX(air_date)).
//  6. MediaResolver.Resolve per season poster → sha256 hash.
func (sc *SeasonsComposer) Compose(ctx context.Context, seriesID domain.SeriesID, lang string) (SeasonsListDTO, error) {
	start := sc.d.Now()

	var freshen FreshenResult
	if sc.d.Freshener != nil {
		freshen, _ = sc.d.Freshener.EnsureFreshScope(
			ctx, seriesID, lang,
			[]freshener.Section{freshener.SectionSkeleton},
			nil,   // seasonNumbers — list view renders no season episodes
			false, // force — TTL respected
			ModeSync,
		)
	}

	canon, err := sc.d.Series.Get(ctx, seriesID)
	if err != nil {
		// Typed SeriesNotFoundError joined with ports.ErrNotFound flows through;
		// the handler maps ports.ErrNotFound → 404. Non-404 errors → 500.
		return SeasonsListDTO{}, fmt.Errorf("seasons canon load: %w", err)
	}

	seasons, err := sc.d.Seasons.ListBySeries(ctx, seriesID)
	if err != nil {
		return SeasonsListDTO{}, fmt.Errorf("seasons list: %w", err)
	}

	// S-C phase 2 — season:N fan-out. Phase 1 (SectionSkeleton, above) syncs the
	// canon + season rows on a cold open; now that we know every season number,
	// drive the freshener for each in ModeSync so season_texts (all langs) land
	// before we read them below. Sections that fit the SyncTimeout budget commit
	// synchronously; sections that time out or error are re-dispatched by the
	// freshener onto its own 180s async path (carry-over, W15-10) — they are NOT
	// dropped, so a cold wide series finishes warming in the background after a
	// SINGLE visit. TTL (episodes_synced_at/SeasonTTL) gates repeats and
	// AdaptivePause protects the TMDB rate limit for wide series.
	// nil-OK dep; errors are degraded (Degraded flag), never fatal.
	if sc.d.Freshener != nil && len(seasons) > 0 {
		seasonSections := make([]freshener.Section, 0, len(seasons))
		for i := range seasons {
			seasonSections = append(seasonSections, freshener.SeasonSection(seasons[i].SeasonNumber))
		}
		if _, ferr := sc.d.Freshener.EnsureFreshScope(
			ctx, seriesID, lang,
			seasonSections,
			nil,   // seasonNumbers — holder derives them from the sections
			false, // force — TTL respected
			ModeSync,
		); ferr != nil {
			sc.d.Logger.WarnContext(ctx, "seasons_freshener_error",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("lang", lang),
				slog.Int("season_count", len(seasons)),
				slog.String("err", ferr.Error()))
			freshen.Degraded = true
		}
	}

	// Localized names/overviews — read AFTER the season:N fan-out so a cold open
	// observes the freshly-written rows. S-E2: name/overview come ONLY from
	// season_texts (requested lang → en-US); a repo miss / nil map renders blank
	// (canon is no longer a tier-3 fallback), never fails the page.
	texts, terr := sc.d.SeasonTexts.ListBySeriesWithFallback(ctx, seriesID, lang)
	if terr != nil {
		sc.d.Logger.WarnContext(ctx, "seasons_texts_fallback_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.String("error", terr.Error()))
		texts = nil // degrade to blank names, do NOT fail the page
	}

	// S-C2 — per-season localized posters (lang → en-US; canon seasons.poster_asset
	// = tier 3, applied per-row below). nil-safe: miss → nil map → canon poster.
	var mediaTexts map[int]series.SeasonMediaText
	if sc.d.SeasonMediaTexts != nil {
		var merr error
		mediaTexts, merr = sc.d.SeasonMediaTexts.ListBySeriesWithFallback(ctx, seriesID, lang)
		if merr != nil {
			sc.d.Logger.WarnContext(ctx, "seasons_media_texts_fallback_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("lang", lang),
				slog.String("error", merr.Error()))
			mediaTexts = nil
		}
	}

	// Per-season episode aggregate (episode_count + air_date_end).
	aggs, aerr := sc.d.Aggregates.AggregateBySeries(ctx, seriesID)
	if aerr != nil {
		sc.d.Logger.WarnContext(ctx, "seasons_aggregate_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("error", aerr.Error()))
		aggs = nil // degrade to canon episode_count / air_date, do NOT fail
	}

	out := SeasonsListDTO{
		SeriesID: seriesID,
		Seasons:  make([]SeasonSummary, 0, len(seasons)),
		SyncedAt: canon.UpdatedAt,
	}

	// W15-9 — track the served language of the season names. requestedLang is
	// the normalized request; sawRequested records a name row in that language,
	// firstFallback the first name row served in another (fallback) language.
	requestedLang := resolveLang(lang)
	var firstFallback string
	sawRequested := false

	for i := range seasons {
		s := seasons[i]

		// S-E2 — name/overview resolved ONLY from season_texts (requested
		// lang → en-US via ListBySeriesWithFallback). Canon seasons.name /
		// seasons.overview are no longer a fallback tier; a miss renders
		// blank and the FE shows the numbered-label placeholder (#973).
		var name, overview string
		if txt, ok := texts[s.SeasonNumber]; ok {
			if txt.Name != nil {
				name = *txt.Name
				if txt.Language == requestedLang {
					sawRequested = true
				} else if firstFallback == "" {
					firstFallback = txt.Language
				}
			}
			if txt.Overview != nil {
				overview = *txt.Overview
			}
		}

		// air_date_start: canon season air_date, else aggregate MIN.
		airStart := s.AirDate
		// air_date_end: aggregate MAX (no canon source column).
		var airEnd *time.Time
		// episode_count: canon TMDB-declared, else actual aggregate row count.
		epCount := 0
		if s.EpisodeCount != nil {
			epCount = *s.EpisodeCount
		}
		if agg, ok := aggs[s.SeasonNumber]; ok {
			airEnd = agg.LastAirDate
			if airStart == nil {
				airStart = agg.FirstAirDate
			}
			if epCount == 0 {
				epCount = agg.EpisodeCount
			}
		}

		// S-E3a — season poster comes ONLY from season_media_texts
		// (lang → en-US); canon season carries no poster_asset. A miss →
		// nil → FE monogram.
		var posterSrc *string
		if mt, ok := mediaTexts[s.SeasonNumber]; ok && mt.PosterAsset != nil && *mt.PosterAsset != "" {
			posterSrc = mt.PosterAsset
		}
		summary := SeasonSummary{
			SeasonNumber: s.SeasonNumber,
			Name:         name,
			AirDateStart: airStart,
			AirDateEnd:   airEnd,
			EpisodeCount: epCount,
			Overview:     overview,
			PosterAsset:  sc.d.MediaResolver.Resolve(ctx, posterSrc, "w342", "poster_w342"),
		}
		out.Seasons = append(out.Seasons, summary)
	}

	// W15-9 — a fallback name anywhere wins (first-seen); otherwise the
	// requested language when at least one name was served; else empty.
	switch {
	case firstFallback != "":
		out.ServedLanguage = firstFallback
	case sawRequested:
		out.ServedLanguage = requestedLang
	}

	out.Degraded = sc.computeDegraded(canon, freshen)

	sc.d.Logger.InfoContext(ctx, "series_seasons_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
		slog.Int("season_count", len(out.Seasons)),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	return out, nil
}

// computeDegraded mirrors SkeletonComposer.computeDegraded — cold canon rows
// surface tmdb_series; a timed-out freshener surfaces freshener.
func (sc *SeasonsComposer) computeDegraded(canon series.Canon, freshen FreshenResult) []string {
	var degraded []string
	if canon.Hydration != series.HydrationFull {
		degraded = append(degraded, "tmdb_series")
	}
	if freshen.Degraded {
		degraded = append(degraded, "freshener")
	}
	return degraded
}
