// Package scan — resolveOrCreateSeries implements the TMDB → TVDB →
// orphan lookup chain for E-1's Sonarr sync (PRD v4 §5.8). Pure
// resolution logic; no merge policy and no per-field writes — the
// caller (SyncSeriesFromSonarr) takes the resolved canon and applies
// MergeSeries (story 207 enrichment.MergePolicy) before persisting.
package scan

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ResolveOrCreateSeries returns the canonical series row for the
// given Sonarr payload. Resolution order (PRD §5.8):
//
//  1. By tmdb_id (Sonarr ships this for matched shows).
//  2. By tvdb_id (Sonarr is TVDB-native — always present).
//  3. Create orphan series (tmdb_id=NULL, tvdb_id=Sonarr's,
//     hydration=stub) — title is Sonarr's title.
//
// The returned canon carries only the resolution fields populated
// (id, external ids, title for new rows). The caller MUST apply
// MergeSeries against this canon to land the Sonarr-grain fields
// before writing back through SeriesCanonRepository.Upsert.
func ResolveOrCreateSeries(
	ctx context.Context,
	repo SeriesCanonRepository,
	p sonarr.SeriesPayload,
) (series.Canon, error) {
	tmdbID := nonZeroTMDBIDPtr(p.TMDBID)
	tvdbID := nonZeroTVDBIDPtr(p.TVDBID)
	imdbID := nonEmptyIMDBIDPtr(p.IMDBID)

	existing, err := repo.FindByExternalIDs(ctx, tmdbID, tvdbID, imdbID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ports.ErrNotFound) {
		return series.Canon{}, fmt.Errorf("resolve series: %w", err)
	}

	now := time.Now().UTC()
	title := p.Title
	if title == "" {
		title = fmt.Sprintf("sonarr:%d", p.ID)
	}
	return series.Canon{
		TMDBID:    tmdbID,
		TVDBID:    tvdbID,
		IMDBID:    imdbID,
		Title:     title,
		Hydration: series.HydrationStub,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// nonZeroTMDBIDPtr is the typed-primitive variant — story 403 A-5d-2.
func nonZeroTMDBIDPtr(v domain.TMDBID) *domain.TMDBID {
	if v == 0 {
		return nil
	}
	return &v
}

// nonZeroTVDBIDPtr is the typed-primitive variant — story 404 A-5d-3.
func nonZeroTVDBIDPtr(v domain.TVDBID) *domain.TVDBID {
	if v == 0 {
		return nil
	}
	return &v
}

// nonEmptyIMDBIDPtr is the typed-primitive variant — story 402 A-5d-1.
func nonEmptyIMDBIDPtr(v domain.IMDBID) *domain.IMDBID {
	if v == "" {
		return nil
	}
	return &v
}
