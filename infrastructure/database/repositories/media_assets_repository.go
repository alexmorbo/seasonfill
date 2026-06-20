package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
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
			return media.Asset{}, errors.Join(
				&sharedErrors.MediaAssetNotFoundError{Kind: "hash", Key: hash},
				ports.ErrNotFound,
			)
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
			return media.Asset{}, errors.Join(
				&sharedErrors.MediaAssetNotFoundError{Kind: "source_url", Key: url},
				ports.ErrNotFound,
			)
		}
		return media.Asset{}, fmt.Errorf("get media_asset by url: %w", err)
	}
	return modelToAsset(m), nil
}

// HashForSourceURL returns the sha256 hash of the media_assets row matching
// source_url AND status='stored'. Used by the seriesdetail composer to
// translate a raw TMDB image path + size into the wire field (a sha256 hex
// the frontend hands to /api/v1/media/:hash).
//
// Returns ports.ErrNotFound for the miss case (no row, or row exists but
// status != 'stored'). Callers MUST treat ErrNotFound as "leave the DTO field
// nil" — frontend renders a monogram fallback.
//
// Implementation uses the unique idx_media_assets_source_url plus a tiny WHERE
// status='stored' filter; planner serves the read off the index.
func (r *MediaAssetsRepository) HashForSourceURL(ctx context.Context, sourceURL string) (string, error) {
	if sourceURL == "" {
		return "", ports.ErrNotFound
	}
	var hash string
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("media_assets").
		Select("hash").
		Where("source_url = ? AND status = ?", sourceURL, string(media.StatusStored)).
		Limit(1).
		Row().
		Scan(&hash)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, sql.ErrNoRows) {
			return "", errors.Join(
				&sharedErrors.MediaAssetNotFoundError{Kind: "source_url", Key: sourceURL},
				ports.ErrNotFound,
			)
		}
		return "", fmt.Errorf("hash for source_url: %w", err)
	}
	if hash == "" {
		return "", ports.ErrNotFound
	}
	return hash, nil
}

// EnsurePending writes a media_assets row keyed by hash with status='pending',
// source_url, kind, created_at=now — IFF the row doesn't already exist.
// Idempotent: ON CONFLICT (hash) DO NOTHING — an existing row's status (which
// may be 'stored' or 'failed' from a prior fetch) is preserved.
//
// Story 320: the seriesdetail composer calls this on hero poster/backdrop
// lookup miss BEFORE returning the deterministic eager hash, so the handler
// (story 321 GET /api/v1/media/:hash) can recover the source URL + kind off
// the pending row and synchronously fetch on demand.
//
// Validation: empty hash / sourceURL → fmt.Errorf — programming bugs surface,
// not silently swallowed. Hash length is NOT enforced here (the underlying
// model is text PRIMARY KEY); media.Asset.Validate() is the source of truth
// for hash format and only runs on Upsert.
func (r *MediaAssetsRepository) EnsurePending(ctx context.Context, hash, sourceURL, kind string) error {
	if hash == "" {
		return fmt.Errorf("ensure pending media_asset: empty hash")
	}
	if sourceURL == "" {
		return fmt.Errorf("ensure pending media_asset: empty source_url")
	}
	now := r.clock()
	m := database.MediaAssetModel{
		Hash:      hash,
		SourceURL: sourceURL,
		Kind:      kind,
		Status:    string(media.StatusPending),
		CreatedAt: now,
	}
	// ON CONFLICT (hash) DO NOTHING — an existing row keeps its status,
	// source_url, kind, content_type, size_bytes, fetched_at, created_at.
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hash"}},
			DoNothing: true,
		}).
		Create(&m).Error
	if err != nil {
		return fmt.Errorf("ensure pending media_asset: %w", err)
	}
	return nil
}

// GetSourceURLByHash returns the (source_url, kind, status) tuple for the
// media_assets row keyed by hash. Used by the GET /api/v1/media/:hash
// handler (story 321) to recover the upstream URL when the store doesn't
// have the bytes yet — the handler then calls OnDemandFetcher.FetchSync to
// pull the bytes synchronously before serving.
//
// Returns ports.ErrNotFound when no row exists; the handler maps that to
// 404. Errors other than "not found" are wrapped and surfaced to the
// handler's 5xx path.
func (r *MediaAssetsRepository) GetSourceURLByHash(ctx context.Context, hash string) (string, string, media.Status, error) {
	if hash == "" {
		return "", "", "", ports.ErrNotFound
	}
	var row struct {
		SourceURL string `gorm:"column:source_url"`
		Kind      string `gorm:"column:kind"`
		Status    string `gorm:"column:status"`
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("media_assets").
		Select("source_url, kind, status").
		Where("hash = ?", hash).
		Limit(1).
		Row().
		Scan(&row.SourceURL, &row.Kind, &row.Status)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, sql.ErrNoRows) {
			return "", "", "", errors.Join(
				&sharedErrors.MediaAssetNotFoundError{Kind: "hash", Key: hash},
				ports.ErrNotFound,
			)
		}
		return "", "", "", fmt.Errorf("get source_url by hash: %w", err)
	}
	return row.SourceURL, row.Kind, media.Status(row.Status), nil
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

// IterateColdAssets walks media_assets rows whose last_access_at is
// older than cutoff (or never accessed). Page-by-page via id-less
// keyset on hash — the table is keyed on hash. fn is called with the
// row's (hash, source_url, content_type) tuple. Returning a non-nil
// error from fn aborts the iteration.
//
// Story 218 (E-2). The store key isn't a column on media_assets;
// callers derive it via mediastore.Key(source_url, ext).
func (r *MediaAssetsRepository) IterateColdAssets(
	ctx context.Context,
	lastAccessCutoff time.Time,
	pageSize int,
	fn func(hash, sourceURL, contentType string) error,
) error {
	if pageSize <= 0 {
		pageSize = 1000
	}
	type row struct {
		Hash        string  `gorm:"column:hash"`
		SourceURL   string  `gorm:"column:source_url"`
		ContentType *string `gorm:"column:content_type"`
	}
	lastHash := ""
	for {
		var batch []row
		err := dbFromContext(ctx, r.db).WithContext(ctx).
			Table("media_assets").
			Select("hash, source_url, content_type").
			Where("hash > ?", lastHash).
			Where("(last_access_at IS NULL OR last_access_at < ?)", lastAccessCutoff).
			Order("hash ASC").
			Limit(pageSize).
			Find(&batch).Error
		if err != nil {
			return fmt.Errorf("iterate cold assets: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		for _, b := range batch {
			ct := ""
			if b.ContentType != nil {
				ct = *b.ContentType
			}
			if cerr := fn(b.Hash, b.SourceURL, ct); cerr != nil {
				return cerr
			}
			lastHash = b.Hash
		}
		if len(batch) < pageSize {
			return nil
		}
	}
}

// DeleteByHash hard-deletes one media_assets row. Story 218 (E-2).
// Empty hash returns an error so a programming bug surfaces during
// development; a missing row is silently a no-op (idempotent).
func (r *MediaAssetsRepository) DeleteByHash(ctx context.Context, hash string) error {
	if hash == "" {
		return fmt.Errorf("delete media_asset: empty hash")
	}
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("hash = ?", hash).
		Delete(&database.MediaAssetModel{})
	if res.Error != nil {
		return fmt.Errorf("delete media_asset: %w", res.Error)
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
