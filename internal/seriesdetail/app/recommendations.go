// Package seriesdetail — see ports.go header.
//
// recommendations.go (Story 530 — decomposition 2/3). Composer.GetRecommendations
// returns the recommendations[] subset served by
// GET /api/v1/series/:id/recommendations. Loads cache → canon-id →
// Recommendations.ListBySeries → batch SeriesPort.Get + in_library probe
// — same branch the monolith composer runs at composer.go:648, lifted
// into a public method + paginated.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// Recommendations is the value object returned by Composer.GetRecommendations.
// Items carry the canonical canon row + the in-library scope; the rest
// mapper turns each into a dto.Recommendation. TotalCount is the count
// of renderable items (canon resolvable), NOT the raw id-list length —
// stubs that haven't been hydrated yet are silently skipped to match
// the monolith composer's behaviour at loadRecommendations.
type Recommendations struct {
	Instance       domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
	SeriesID       domain.SeriesID
	Items          []RecommendationDetail
	TotalCount     int
	HasMore        bool
	Degraded       []string
}

// Pagination bounds. Caller layer clamps; this is the source of truth.
const (
	RecommendationsLimitDefault = 20
	RecommendationsLimitMax     = 50
	RecommendationsLimitMin     = 1
)

// GetRecommendations returns the paginated recommendations slice for a
// series. Hard-required path: cache → canon-id. Recommendations list
// failure degrades with a "tmdb_series" tag rather than failing the
// response — a slow / cold TMDB recommendations row is the same UX as
// a slow descriptive blurb (Story 529 §3).
//
// lang (Story 565 B-recs-lang) — BCP-47 tag used to override each
// rec's canon.Title with the localised series_texts row when present.
// Empty / invalid values normalise to en-US via resolveLang (defensive
// bounds check + trim). No new port failures degrade the response —
// series_texts load failure falls back to canon titles + a warn log.
//
// limit/offset are pre-clamped by the caller — this method asserts they
// are within bounds and falls back to safe defaults if they are not.
// This makes the method safe to call directly from tests + future
// internal callers without re-validating.
func (c *Composer) GetRecommendations(
	ctx context.Context,
	instanceName domain.InstanceName,
	sonarrSeriesID domain.SonarrSeriesID,
	lang string,
	limit, offset int,
) (*Recommendations, error) {
	if limit <= 0 {
		limit = RecommendationsLimitDefault
	}
	if limit > RecommendationsLimitMax {
		limit = RecommendationsLimitMax
	}
	if offset < 0 {
		offset = 0
	}
	lang = resolveLang(lang)

	cache, err := c.d.SeriesCache.Get(ctx, instanceName, sonarrSeriesID)
	if err != nil {
		return nil, fmt.Errorf("series_cache lookup: %w", err)
	}
	if cache.SeriesID == nil || *cache.SeriesID == 0 {
		return nil, errors.Join(
			&sharedErrors.SeriesCacheNotFoundError{
				InstanceName:   instanceName,
				SonarrSeriesID: sonarrSeriesID,
			},
			ports.ErrNotFound,
		)
	}
	seriesID := *cache.SeriesID

	out := &Recommendations{
		Instance:       instanceName,
		SonarrSeriesID: sonarrSeriesID,
		SeriesID:       seriesID,
		Items:          []RecommendationDetail{},
		Degraded:       []string{},
	}

	ids, err := c.d.Recommendations.ListBySeries(ctx, seriesID)
	if err != nil {
		c.d.Logger.WarnContext(ctx, "recommendations_list_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("err", err.Error()))
		out.Degraded = append(out.Degraded, "tmdb_series")
		return out, nil
	}

	// Story 551 (E-1 Z2) — batch the canon stub hydration. Mirrors the
	// fat composer's loadRecommendations after Story 551. Order
	// preservation is enforced by iterating `ids` in input sequence
	// when projecting; misses are silently dropped (stub-skip parity
	// with the prior shape).
	resolved := make([]RecommendationDetail, 0, len(ids))
	canons, lerr := c.d.Series.ListByIDs(ctx, ids)
	if lerr != nil {
		// Treat a batch failure the same way the prior shape treated a
		// per-row error: degrade silently (the `for _, recID := range ids`
		// loop swallowed the `sgerr` already). Surfacing it as a
		// `tmdb_series` degraded flag would over-report — the lookup
		// table is the local canon, not TMDB. Log + continue with an
		// empty resolved slice.
		c.d.Logger.WarnContext(ctx, "recommendations_canons_batch_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.Int("rec_count", len(ids)),
			slog.String("err", lerr.Error()))
	} else {
		byID := make(map[domain.SeriesID]series.Canon, len(canons))
		for _, canon := range canons {
			byID[canon.ID] = canon
		}

		// Story 565 (B-recs-lang) — batch-load localised titles for
		// the resolved rec ids. Failure degrades quietly to canon
		// titles + warn log; missing entries in the map are the norm
		// for cold series (freshener hasn't populated ru-RU yet).
		var localised map[domain.SeriesID]series.SeriesText
		if c.d.SeriesTexts != nil && len(ids) > 0 {
			resolvedIDs := make([]domain.SeriesID, 0, len(ids))
			for _, recID := range ids {
				if _, ok := byID[recID]; ok {
					resolvedIDs = append(resolvedIDs, recID)
				}
			}
			if len(resolvedIDs) > 0 {
				var terr error
				localised, terr = c.d.SeriesTexts.ListByIDsWithFallback(ctx, resolvedIDs, lang)
				if terr != nil {
					c.d.Logger.WarnContext(ctx, "recommendations_texts_batch_failed",
						slog.Int64("series_id", int64(seriesID)),
						slog.String("lang", lang),
						slog.Int("rec_count", len(resolvedIDs)),
						slog.String("err", terr.Error()))
					// nil map — projection below no-ops the override.
					localised = nil
				}
			}
		}

		for _, recID := range ids {
			canon, ok := byID[recID]
			if !ok {
				continue
			}
			// Story 565 — override canon.Title with the localised row
			// when present (non-nil, non-empty). Empty / missing keeps
			// canon.Title. Same posture as composer.go branch a in
			// GetDetail: series_texts is the source of truth for the
			// display title when a row exists.
			if localised != nil {
				if t, has := localised[recID]; has && t.Title != nil && *t.Title != "" {
					canon.Title = *t.Title
				}
			}
			rd := RecommendationDetail{Series: canon}
			if c.d.SeriesCacheLookup != nil {
				caches, _ := c.d.SeriesCacheLookup.ListBySeriesID(ctx, recID)
				if len(caches) > 0 {
					rd.InLibrary = true
					rd.InstanceName = caches[0].InstanceName
					rd.SonarrSeriesID = caches[0].SonarrSeriesID
				}
			}
			resolved = append(resolved, rd)
		}
	}

	out.TotalCount = len(resolved)

	// Apply pagination + run the media resolver on the visible slice
	// only — out-of-window posters don't need hash translation.
	if offset >= len(resolved) {
		out.Items = []RecommendationDetail{}
		out.HasMore = false
	} else {
		end := min(offset+limit, len(resolved))
		out.Items = resolved[offset:end]
		out.HasMore = end < len(resolved)
		c.resolveRecommendationsMedia(ctx, out.Items)
	}

	c.d.Logger.InfoContext(ctx, "series_recommendations_composed",
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrSeriesID)),
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
		slog.Int("limit", limit),
		slog.Int("offset", offset),
		slog.Int("total_count", out.TotalCount),
		slog.Int("items_returned", len(out.Items)),
		slog.Bool("has_more", out.HasMore))
	return out, nil
}

// resolveRecommendationsMedia mirrors composer.go:896-898 — translates
// raw TMDB poster paths into media hashes. Nil-OK resolver short-circuits.
func (c *Composer) resolveRecommendationsMedia(ctx context.Context, items []RecommendationDetail) {
	r := c.d.MediaResolver
	if r == nil {
		return
	}
	for i := range items {
		items[i].Series.PosterAsset = r.Resolve(ctx, items[i].Series.PosterAsset, "w342", "poster_w342")
	}
}
