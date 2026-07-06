// Package seriesdetail — see ports.go header.
//
// overview.go (Story 529 — decomposition 1/3). Composer.GetOverview
// returns the description + keywords + awards subset served by
// GET /api/v1/series/:id/overview. Loads only 3 things (cache → canon →
// series_texts → keywords) — far cheaper than Composer.Get's 9-branch
// fan-out.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// Overview is the slim value object returned by Composer.GetOverview.
// Mirrors dto.OverviewAside field-by-field — the rest mapper is trivial.
type Overview struct {
	Instance            domain.InstanceName
	SonarrSeriesID      domain.SonarrSeriesID
	SeriesID            domain.SeriesID
	Lang                string
	Description         string
	DescriptionLanguage string
	Keywords            []taxonomy.Keyword
	Awards              *string // nil when OMDb hasn't synced or = "N/A"
	Degraded            []string
}

// GetOverview returns the lightweight overview slice for a series.
// Hard-required path: cache → canon. Optional best-effort loads
// (texts, keywords); failures degrade with a "tmdb_series" tag rather
// than failing the response.
func (c *Composer) GetOverview(
	ctx context.Context,
	instanceName domain.InstanceName,
	sonarrSeriesID domain.SonarrSeriesID,
	lang string,
) (*Overview, error) {
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

	canon, err := c.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("series canon load: %w", err)
	}

	out := &Overview{
		Instance:       instanceName,
		SonarrSeriesID: sonarrSeriesID,
		SeriesID:       seriesID,
		Lang:           lang,
		Degraded:       []string{},
	}

	if t, terr := c.d.SeriesTexts.GetWithFallback(ctx, seriesID, lang); terr == nil {
		if t.Overview != nil {
			out.Description = *t.Overview
		}
		out.DescriptionLanguage = t.Language
	} else if !errors.Is(terr, ports.ErrNotFound) {
		c.d.Logger.WarnContext(ctx, "overview_texts_load_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("err", terr.Error()))
		out.Degraded = append(out.Degraded, "tmdb_series")
	}

	if kwIDs, kerr := c.d.Keywords.ListBySeries(ctx, seriesID); kerr == nil {
		if len(kwIDs) > 0 {
			// Story 552 (E-1 Z3) — batch i18n fetch. Same pattern as
			// composer.loadTaxonomy. Failure here is degraded-mode like
			// the original — log + tag tmdb_series, keep the response.
			kws, lerr := c.d.Keywords.ListByIDsWithFallback(ctx, kwIDs, lang)
			if lerr != nil {
				c.d.Logger.WarnContext(ctx, "overview_keywords_load_failed",
					slog.Int64("series_id", int64(seriesID)),
					slog.String("err", lerr.Error()))
				out.Degraded = append(out.Degraded, "tmdb_series")
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
		c.d.Logger.WarnContext(ctx, "overview_keywords_load_failed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("err", kerr.Error()))
		out.Degraded = append(out.Degraded, "tmdb_series")
	}

	if canon.OMDBAwards != nil && *canon.OMDBAwards != "" && *canon.OMDBAwards != "N/A" {
		v := *canon.OMDBAwards
		out.Awards = &v
	}

	c.d.Logger.InfoContext(ctx, "series_overview_composed",
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrSeriesID)),
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
		slog.Int("keyword_count", len(out.Keywords)),
		slog.Bool("has_awards", out.Awards != nil),
		slog.Bool("has_description", out.Description != ""))
	return out, nil
}
