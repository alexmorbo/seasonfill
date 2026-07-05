// Package seriesdetail — see ports.go header.
//
// resolve_usecase.go (BE-3, card-unification). Lazy resolve-or-create of
// a canonical series.id from a TMDB id so the unified series card can
// always click through to the internal /series/:id page. Person-page
// "other credits" TV rows carry only a tmdb_id (no canon series_id yet);
// the FE calls GET /series/resolve on click and this use case returns an
// existing canon id or materialises a minimal stub + enqueues enrichment.
//
// Reuses the existing resolve-or-create-by-tmdb primitives rather than
// duplicating: GetByTMDBID (natural-key lookup) + UpsertStub (COALESCE-
// protected minimal canon insert, idempotent on the tmdb_id partial
// unique index) on enrichment's SeriesRepository, and the OnDemandEnricher
// PriorityHot enqueue that TMDBFallbackUseCase already uses to lift stub
// rows on first view.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ErrInvalidTMDBID is returned when the requested tmdb_id is not a
// positive integer. The handler maps it to 400.
var ErrInvalidTMDBID = errors.New("resolve: tmdb_id must be a positive integer")

// ResolveSeriesStore is the narrow resolve-or-create-by-tmdb port.
// enrichpersistence.SeriesRepository satisfies both methods:
//   - GetByTMDBID returns the canon row for a tmdb_id (ports.ErrNotFound
//     wrapped on miss).
//   - UpsertStub inserts a minimal hydration='stub' canon keyed by
//     tmdb_id; the partial unique index makes it idempotent (a second
//     call returns the same id without a duplicate row).
type ResolveSeriesStore interface {
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (series.Canon, error)
	UpsertStub(ctx context.Context, c series.Canon) (domain.SeriesID, error)
}

// ResolveUseCase resolves a TMDB id to a canonical series.id, creating a
// stub + enqueuing enrichment on first sight.
type ResolveUseCase struct {
	store    ResolveSeriesStore
	enricher OnDemandEnricher // nil-OK — enqueue is skipped when enrichment is disabled at boot
	log      *slog.Logger
}

// NewResolveUseCase constructs the use case. store is required; enricher
// is nil-OK (matches the TMDBFallbackUseCase seam); log=nil falls back to
// slog.Default.
func NewResolveUseCase(store ResolveSeriesStore, enricher OnDemandEnricher, log *slog.Logger) (*ResolveUseCase, error) {
	if store == nil {
		return nil, errors.New("resolve: store required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ResolveUseCase{store: store, enricher: enricher, log: log}, nil
}

// ResolveByTMDB returns the canonical series.id for tmdbID. Behaviour:
//   - tmdbID <= 0 → ErrInvalidTMDBID.
//   - canon row already exists → return its id (no write, no enqueue).
//   - unknown tmdbID → create a minimal hydration='stub' canon +
//     enqueue enrichment at PriorityHot, then return the new id.
//
// Idempotent: a second call for the same tmdbID takes the existing-row
// branch and returns the same id. Concurrent first-time calls converge
// via UpsertStub's OnConflict(tmdb_id) — no duplicate canon row.
func (u *ResolveUseCase) ResolveByTMDB(ctx context.Context, tmdbID domain.TMDBID) (domain.SeriesID, error) {
	if tmdbID <= 0 {
		return 0, ErrInvalidTMDBID
	}
	canon, err := u.store.GetByTMDBID(ctx, tmdbID)
	if err == nil {
		return canon.ID, nil
	}
	if !errors.Is(err, ports.ErrNotFound) {
		return 0, fmt.Errorf("resolve: lookup tmdb %d: %w", int64(tmdbID), err)
	}

	tid := tmdbID
	newID, err := u.store.UpsertStub(ctx, series.Canon{
		TMDBID:    &tid,
		Hydration: series.HydrationStub,
	})
	if err != nil {
		return 0, fmt.Errorf("resolve: create stub tmdb %d: %w", int64(tmdbID), err)
	}
	if u.enricher != nil {
		u.enricher.EnqueueIfStale(newID, series.HydrationStub)
	}
	u.log.InfoContext(ctx, "series_resolve_stub_created",
		slog.Int64("tmdb_id", int64(tmdbID)),
		slog.Int64("series_id", int64(newID)))
	return newID, nil
}
