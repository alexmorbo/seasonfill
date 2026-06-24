// Package seriesdetail — see ports.go header.
//
// tmdb_fallback_usecase.go (Story 491 / N-1a). TMDBFallbackUseCase
// returns a canonical-only Detail for series not present in any Sonarr
// library. It reads only from the local series row (canon); it does NOT
// synchronously hit TMDB. Discovery (N-2) is the path that lazy-stub-
// upserts canon rows from TMDB ids — this UC trusts that the canon row
// already exists.
//
// Returned Detail has:
//   - Canon copied from series row (Hero metadata)
//   - Empty Seasons / Cast / Recommendations / Queue / QueueRecords
//   - degraded[] populated for any source the canon row is missing data
//     for (hydration=stub → tmdb_series degraded).
//   - SyncedAt = now.
//   - MediaResolver hash translation applied (poster + backdrop) on the
//     synchronous-resolve fast path so the FE can render the hero card.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// TMDBFallbackDeps — narrow ports.
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
}

// TMDBFallbackUseCase returns canon-only Details.
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
	canon, err := u.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("tmdbfallback: canon load: %w", err)
	}
	lang = resolveLang(lang)
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
	u.d.Logger.InfoContext(ctx, "tmdb_fallback_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("hydration", string(canon.Hydration)),
		slog.String("lang", lang),
	)
	return d, nil
}
