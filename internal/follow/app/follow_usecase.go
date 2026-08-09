// Package app is the follow/watchlist application layer (ADR-0015 Ф3 C1).
// Follow = "I want to know when this airs" (NOT "watching" — watch-status is
// won't-do, П13). Following triggers full enrichment (D1 hydration) so the
// calendar of a followed-not-in-library series is complete (F-04); enrollment
// reuses the shipped OnDemandEnricher, no new enrichment logic.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ErrInvalidSeriesID — series_id was not a positive integer. Handler → 400.
var ErrInvalidSeriesID = errors.New("follow: series_id must be a positive integer")

// ErrSeriesNotFound — no canon series row for the posted series_id. Handler →
// 404. A follow request on a TMDB-only card must first resolve tmdb → canon
// via GET /series/resolve (which creates the stub + enrolls enrichment); the
// FE follows the resolved canon id.
var ErrSeriesNotFound = errors.New("follow: series not found")

// FollowedItem is one watchlist card (read model for GET /follow).
type FollowedItem struct {
	SeriesID    domain.SeriesID
	TMDBID      *domain.TMDBID
	Title       string
	PosterAsset *string
	Year        *int
	FollowedAt  time.Time
}

// SeriesReader loads a canon series by id. Satisfied by
// enrichpersistence.SeriesRepository.Get (returns ports.ErrNotFound on miss).
type SeriesReader interface {
	Get(ctx context.Context, id domain.SeriesID) (series.Canon, error)
}

// FollowStore is the followed_series persistence port.
type FollowStore interface {
	Follow(ctx context.Context, seriesID domain.SeriesID) error
	Unfollow(ctx context.Context, seriesID domain.SeriesID) error
	ListFollowed(ctx context.Context, lang string) ([]FollowedItem, error)
}

// Enricher is the narrow enrollment port. Satisfied by
// adapters.OnDemandEnricherHolder (the SAME instance ResolveUseCase uses).
// nil-OK — enqueue is skipped when enrichment is disabled at boot.
type Enricher interface {
	EnqueueIfStale(seriesID domain.SeriesID, hydration series.Hydration)
}

// FollowUseCase orchestrates follow/unfollow/list.
type FollowUseCase struct {
	series   SeriesReader
	store    FollowStore
	enricher Enricher // nil-OK
	log      *slog.Logger
}

// NewFollowUseCase constructs the use case. series + store required; enricher
// nil-OK; log=nil → slog.Default.
func NewFollowUseCase(series SeriesReader, store FollowStore, enricher Enricher, log *slog.Logger) (*FollowUseCase, error) {
	if series == nil {
		return nil, errors.New("follow: series reader required")
	}
	if store == nil {
		return nil, errors.New("follow: store required")
	}
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "follow")
	}
	return &FollowUseCase{series: series, store: store, enricher: enricher, log: log}, nil
}

// Follow marks seriesID as followed (idempotent) and enrolls it into
// enrichment (D1 hydration). 404 if the canon series does not exist.
func (u *FollowUseCase) Follow(ctx context.Context, seriesID domain.SeriesID) error {
	if seriesID <= 0 {
		return ErrInvalidSeriesID
	}
	canon, err := u.series.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return ErrSeriesNotFound
		}
		return fmt.Errorf("follow: load series %d: %w", int64(seriesID), err)
	}
	if err := u.store.Follow(ctx, seriesID); err != nil {
		return fmt.Errorf("follow: persist %d: %w", int64(seriesID), err)
	}
	// F-04 — followed series MUST refresh on the Followed tier (10d), not decay
	// to Cold. Kicking enrichment here lifts a stub to full immediately; the
	// tiered picker then keeps air_date fresh.
	if u.enricher != nil {
		u.enricher.EnqueueIfStale(seriesID, canon.Hydration)
	}
	u.log.InfoContext(ctx, "series_followed", slog.Int64("series_id", int64(seriesID)))
	return nil
}

// Unfollow clears the followed flag (idempotent). The now-orphan canon (if
// also not in library and not recommended) is reclaimed by weekly OrphanSeries-GC.
func (u *FollowUseCase) Unfollow(ctx context.Context, seriesID domain.SeriesID) error {
	if seriesID <= 0 {
		return ErrInvalidSeriesID
	}
	if err := u.store.Unfollow(ctx, seriesID); err != nil {
		return fmt.Errorf("unfollow: %d: %w", int64(seriesID), err)
	}
	u.log.InfoContext(ctx, "series_unfollowed", slog.Int64("series_id", int64(seriesID)))
	return nil
}

// ListFollowed returns the watchlist cards, newest first.
func (u *FollowUseCase) ListFollowed(ctx context.Context, lang string) ([]FollowedItem, error) {
	items, err := u.store.ListFollowed(ctx, lang)
	if err != nil {
		return nil, fmt.Errorf("follow: list: %w", err)
	}
	return items, nil
}
