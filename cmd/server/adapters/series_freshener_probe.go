package adapters

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// StalenessProbe is the read-side decision for whether a series_id
// needs refresh. Composer-local because the criteria reflect PRD §5.6
// freshness rules + the per-lang invariant Story 533 added.
type StalenessProbe interface {
	IsStale(ctx context.Context, seriesID domain.SeriesID, lang string) (stale bool, reason string)
}

// SeriesReader is the narrow read port the probe consumes for the
// canon row. *enrichpersistence.SeriesRepository satisfies it.
type SeriesReader interface {
	Get(ctx context.Context, id domain.SeriesID) (catalogseries.Canon, error)
}

// SeriesTextsReader — narrow port reading the localised row with
// language fallback. *enrichpersistence.SeriesTextsRepository satisfies it.
type SeriesTextsReader interface {
	GetWithFallback(ctx context.Context, seriesID domain.SeriesID, language string) (catalogseries.SeriesText, error)
}

// CountByID — generic narrow "how many child rows" port. Used for the
// series_seasons + series_people emptiness check.
type CountByID interface {
	CountBySeries(ctx context.Context, seriesID domain.SeriesID) (int, error)
}

// SeriesFreshenerProbeConfig is the dep surface for the probe.
type SeriesFreshenerProbeConfig struct {
	Series       SeriesReader
	SeriesTexts  SeriesTextsReader
	SeasonsCount CountByID
	PeopleCount  CountByID
	CanonTTL     time.Duration // default 7d
	Logger       *slog.Logger
}

// SeriesFreshenerProbe — production StalenessProbe.
type SeriesFreshenerProbe struct {
	cfg SeriesFreshenerProbeConfig
}

// NewSeriesFreshenerProbe constructs the probe. Series + SeriesTexts +
// SeasonsCount + PeopleCount are required; CanonTTL defaults to 7d.
func NewSeriesFreshenerProbe(cfg SeriesFreshenerProbeConfig) (*SeriesFreshenerProbe, error) {
	if cfg.Series == nil || cfg.SeriesTexts == nil ||
		cfg.SeasonsCount == nil || cfg.PeopleCount == nil {
		return nil, errors.New("seriesfreshenerprobe: every reader is required")
	}
	if cfg.CanonTTL <= 0 {
		cfg.CanonTTL = 7 * 24 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &SeriesFreshenerProbe{cfg: cfg}, nil
}

// IsStale runs the checks in order; first hit wins.
func (p *SeriesFreshenerProbe) IsStale(ctx context.Context, seriesID domain.SeriesID, lang string) (bool, string) {
	canon, err := p.cfg.Series.Get(ctx, seriesID)
	if err != nil {
		// Defensive — caller should have already validated existence.
		// Treat as stale "no_canon" so the freshener proceeds to TMDB
		// (which itself surfaces 404 if the worker can't load the row).
		return true, "no_canon"
	}
	if canon.Hydration != catalogseries.HydrationFull {
		return true, "stub"
	}
	if canon.EnrichmentTMDBSyncedAt == nil {
		return true, "never"
	}
	if time.Since(*canon.EnrichmentTMDBSyncedAt) > p.cfg.CanonTTL {
		return true, "ttl"
	}
	if n, err := p.cfg.SeasonsCount.CountBySeries(ctx, seriesID); err == nil && n == 0 {
		return true, "empty_seasons"
	}
	if n, err := p.cfg.PeopleCount.CountBySeries(ctx, seriesID); err == nil && n == 0 {
		return true, "empty_people"
	}
	normLang := strings.TrimSpace(lang)
	if normLang != "" && !strings.EqualFold(normLang, "en-US") {
		row, terr := p.cfg.SeriesTexts.GetWithFallback(ctx, seriesID, normLang)
		if terr == nil && !strings.EqualFold(row.Language, normLang) {
			return true, "missing_lang"
		}
		if terr != nil && errors.Is(terr, ports.ErrNotFound) {
			return true, "missing_lang"
		}
	}
	return false, "fresh"
}
