package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/media"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

// MediaAssetsRepository persists the media_assets table (migration
// 000024). One row per content-addressed stored object; lifecycle is
// the domain/media.Status state machine. Concurrent Upsert is
// idempotent — primary key collision triggers the ON CONFLICT update
// list, keeping the last writer's status / content-type / size.
type MediaAssetsRepository struct {
	db    *gorm.DB
	clock func() time.Time
}

// NewMediaAssetsRepository constructs the repo bound to db. clock is
// the test seam — production callers pass nil.
func NewMediaAssetsRepository(db *gorm.DB) *MediaAssetsRepository {
	return &MediaAssetsRepository{db: db, clock: func() time.Time { return time.Now().UTC() }}
}

// Get returns the row for hash. ports.ErrNotFound on miss; wrapped
// error otherwise. The returned Asset is a value-copy — safe to
// mutate by the caller.
func (r *MediaAssetsRepository) Get(ctx context.Context, hash string) (media.Asset, error) {
	if hash == "" {
		return media.Asset{}, fmt.Errorf("get media_asset: empty hash")
	}
	var m database.MediaAssetModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("hash = ?", hash).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return media.Asset{}, ports.ErrNotFound
		}
		return media.Asset{}, fmt.Errorf("get media_asset: %w", err)
	}
	return modelToAsset(m), nil
}

// GetByUpstreamURL is the secondary-index reader. Optional helper —
// the handler reaches everything by hash, but cmd/server's wiring
// uses this to verify pre-warm idempotency without rebuilding the
// hash on the caller side.
func (r *MediaAssetsRepository) GetByUpstreamURL(ctx context.Context, url string) (media.Asset, error) {
	if url == "" {
		return media.Asset{}, fmt.Errorf("get media_asset: empty url")
	}
	var m database.MediaAssetModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("source_url = ?", url).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return media.Asset{}, ports.ErrNotFound
		}
		return media.Asset{}, fmt.Errorf("get media_asset by url: %w", err)
	}
	return modelToAsset(m), nil
}

// Upsert writes the row keyed by hash. The conflict clause updates
// every non-PK column except created_at — the latter is fixed by the
// first insert. fetched_at is stamped when status transitions to
// stored or failed; last_access_at is left alone (the handler stamps
// it lazily per PRD §6.7).
func (r *MediaAssetsRepository) Upsert(ctx context.Context, a media.Asset) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("upsert media_asset: %w", err)
	}
	now := r.clock()
	m := database.MediaAssetModel{
		Hash:      a.Hash,
		SourceURL: a.UpstreamURL,
		Kind:      a.Kind,
		Status:    string(a.Status),
		CreatedAt: now,
	}
	if a.ContentType != "" {
		ct := a.ContentType
		m.ContentType = &ct
	}
	if a.Size > 0 {
		s := a.Size
		m.SizeBytes = &s
	}
	if a.Status == media.StatusStored || a.Status == media.StatusFailed {
		m.FetchedAt = &now
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "hash"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"source_url", "kind", "status", "content_type",
			"size_bytes", "fetched_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert media_asset: %w", err)
	}
	return nil
}

// TouchLastAccess updates last_access_at to the current clock value.
// Called by the handler on a cache miss to keep GC liveness signals
// fresh. The PRD recommends a once-per-day debounce; F-1 ships the
// unbounded path and lets E-2 layer the debounce on top.
func (r *MediaAssetsRepository) TouchLastAccess(ctx context.Context, hash string) error {
	if hash == "" {
		return nil
	}
	now := r.clock()
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.MediaAssetModel{}).
		Where("hash = ?", hash).
		Update("last_access_at", &now)
	if res.Error != nil {
		return fmt.Errorf("touch last_access_at: %w", res.Error)
	}
	return nil
}

func modelToAsset(m database.MediaAssetModel) media.Asset {
	a := media.Asset{
		Hash:        m.Hash,
		UpstreamURL: m.SourceURL,
		Kind:        m.Kind,
		Status:      media.Status(m.Status),
	}
	if m.ContentType != nil {
		a.ContentType = *m.ContentType
	}
	if m.SizeBytes != nil {
		a.Size = *m.SizeBytes
	}
	return a
}
