// Package seriesdetail — see ports.go header.
//
// global_composer_usecase.go (Story 491 / N-1a). The GlobalComposerUseCase
// is the entry point behind GET /api/v1/series/:id. It resolves the
// canonical series.id to a sorted list of instance names that carry this
// series, picks the lexicographically-first instance as the "preferred"
// view, and delegates to the existing per-instance Composer. When zero
// instances carry the series, it falls back to TMDBFallbackUseCase which
// returns a canon-only Detail (no per-instance branches) — the response
// still carries `in_library_instances=[]` so the FE knows the series is
// not in any library.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ComposerPort is the narrow port the GlobalComposerUseCase needs to
// delegate to the per-instance composer. *Composer satisfies it; tests
// inject a fake. Story 491 / N-1a.
type ComposerPort interface {
	Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID, lang string) (*Detail, error)
}

// TMDBFallbackPort is the narrow port the GlobalComposerUseCase uses
// when no library carries the series. *TMDBFallbackUseCase satisfies
// it; tests inject a fake. Story 491 / N-1a.
type TMDBFallbackPort interface {
	GetCanonical(ctx context.Context, seriesID domain.SeriesID, lang string) (*Detail, error)
}

// GlobalComposerDeps — narrow ports the global composer needs.
//
// CacheLookup: SeriesCacheLookupPort.ListBySeriesID returns []CacheEntry
// with BOTH instance_name AND sonarr_series_id, so we can pick a
// preferred (instance, sonarr_id) tuple and delegate to Composer.Get.
//
// Composer: ComposerPort interface. *Composer satisfies it.
//
// TMDBFallback: TMDBFallbackPort. *TMDBFallbackUseCase satisfies it.
//
// Logger: domain="composer" anchor.
type GlobalComposerDeps struct {
	CacheLookup  SeriesCacheLookupPort
	Composer     ComposerPort
	TMDBFallback TMDBFallbackPort
	Logger       *slog.Logger
}

// GlobalComposerUseCase is the application use case wired to the new
// global /api/v1/series/:id endpoint.
type GlobalComposerUseCase struct {
	d GlobalComposerDeps
}

// NewGlobalComposerUseCase constructs the use case. Composer + CacheLookup
// + TMDBFallback are required. Logger defaults to a domain-tagged slog.
func NewGlobalComposerUseCase(d GlobalComposerDeps) (*GlobalComposerUseCase, error) {
	if d.CacheLookup == nil {
		return nil, errors.New("globalcomposer: CacheLookup required")
	}
	if d.Composer == nil {
		return nil, errors.New("globalcomposer: Composer required")
	}
	if d.TMDBFallback == nil {
		return nil, errors.New("globalcomposer: TMDBFallback required")
	}
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	return &GlobalComposerUseCase{d: d}, nil
}

// Get resolves the series.id to a Detail.
//
// Flow:
//  1. ListBySeriesID(seriesID) → list of cache entries.
//  2. If non-empty: sort by instance_name ASC, pick entries[0]. Delegate
//     to Composer.Get(instance, sonarr_id, lang). Overwrite
//     Detail.InLibraryInstances with the full sorted unique list.
//  3. If empty: delegate to TMDBFallback.GetCanonical(seriesID, lang).
//
// Errors propagate. ports.ErrNotFound on invalid id → handler 404/400.
func (u *GlobalComposerUseCase) Get(ctx context.Context, seriesID domain.SeriesID, lang string) (*Detail, error) {
	if seriesID <= 0 {
		return nil, fmt.Errorf("globalcomposer: invalid series id %d: %w", seriesID, ports.ErrNotFound)
	}
	entries, err := u.d.CacheLookup.ListBySeriesID(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("globalcomposer: list cache: %w", err)
	}

	if len(entries) > 0 {
		// Sort by instance_name ASC for deterministic preferred-instance pick.
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].InstanceName < entries[j].InstanceName
		})
		instances := collectSortedUniqueInstances(entries)
		preferred := entries[0]
		detail, derr := u.d.Composer.Get(ctx, preferred.InstanceName, preferred.SonarrSeriesID, lang)
		if derr != nil {
			return nil, fmt.Errorf("globalcomposer: composer.Get: %w", derr)
		}
		if detail == nil {
			return nil, fmt.Errorf("globalcomposer: composer returned nil detail")
		}
		detail.InLibraryInstances = instances
		u.d.Logger.InfoContext(ctx, "global_series_composed",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("preferred_instance", string(preferred.InstanceName)),
			slog.Int("in_library_count", len(instances)),
			slog.String("lang", lang),
		)
		return detail, nil
	}

	// No cache rows — TMDB-fallback canonical path.
	detail, ferr := u.d.TMDBFallback.GetCanonical(ctx, seriesID, lang)
	if ferr != nil {
		return nil, fmt.Errorf("globalcomposer: tmdb fallback: %w", ferr)
	}
	if detail == nil {
		return nil, fmt.Errorf("globalcomposer: tmdb fallback returned nil detail")
	}
	if detail.InLibraryInstances == nil {
		detail.InLibraryInstances = []domain.InstanceName{}
	}
	u.d.Logger.InfoContext(ctx, "global_series_tmdb_fallback",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
	)
	return detail, nil
}

// collectSortedUniqueInstances returns the unique instance names from
// (already-sorted) cache entries, preserving the input order.
func collectSortedUniqueInstances(entries []series.CacheEntry) []domain.InstanceName {
	seen := make(map[domain.InstanceName]struct{}, len(entries))
	out := make([]domain.InstanceName, 0, len(entries))
	for _, e := range entries {
		if _, ok := seen[e.InstanceName]; ok {
			continue
		}
		seen[e.InstanceName] = struct{}{}
		out = append(out, e.InstanceName)
	}
	return out
}
