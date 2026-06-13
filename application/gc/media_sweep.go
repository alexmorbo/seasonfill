// media_sweep.go — story 218 E-2.
//
// Builds the live-hash set by reading every *_asset column across
// the entity tables, then walks media_assets and deletes any row
// whose hash is NOT in the live set AND whose last_access_at <
// cutoff (default: now - 30d, PRD §6.7). The store Delete is
// best-effort — a store-side failure WARN-logs and leaves the DB
// row, so the next sweep retries.
//
// The media_assets table doesn't carry the store key as a column —
// the key is derived from (source_url, content_type) via
// mediastore.Key + extFromContentType, mirroring the serve path in
// interface/http/handlers/media.go.

package gc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
)

// MediaSweepDeps groups consumer-side ports.
type MediaSweepDeps struct {
	LiveSet LiveHashSource
	Assets  ColdAssetRepo
	Store   mediastore.Store
	Clock   func() time.Time
	Logger  *slog.Logger
	// CooldownAge overrides the default last_access threshold (30d).
	CooldownAge time.Duration
}

// LiveHashSource collects every *_asset hash currently referenced by
// the entity model.
type LiveHashSource interface {
	CollectLiveAssetHashes(ctx context.Context) (map[string]struct{}, error)
}

// ColdAssetRepo is the iterator + deleter half of the media_assets
// repo. We iterate cursor-style (1000-row pages) so a large bucket
// doesn't pin memory. The iterator yields (hash, source_url,
// content_type) — the store key is derived in the sweep loop via
// mediastore.Key + extFromContentType.
type ColdAssetRepo interface {
	IterateColdAssets(
		ctx context.Context,
		lastAccessCutoff time.Time,
		pageSize int,
		fn func(hash, sourceURL, contentType string) error,
	) error
	DeleteByHash(ctx context.Context, hash string) error
}

// Build constructs the WeeklyJob.MediaSweep closure.
func (d MediaSweepDeps) Build() func(ctx context.Context) (MediaSweepResult, error) {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	cooldown := d.CooldownAge
	if cooldown == 0 {
		cooldown = 30 * 24 * time.Hour
	}
	return func(ctx context.Context) (MediaSweepResult, error) {
		live, err := d.LiveSet.CollectLiveAssetHashes(ctx)
		if err != nil {
			return MediaSweepResult{}, fmt.Errorf("collect live hashes: %w", err)
		}
		res := MediaSweepResult{LiveHashCount: len(live)}
		cutoff := clock().Add(-cooldown)

		err = d.Assets.IterateColdAssets(ctx, cutoff, 1000, func(hash, sourceURL, contentType string) error {
			res.Candidates++
			if _, alive := live[hash]; alive {
				return nil
			}
			if sourceURL != "" && d.Store != nil {
				key := mediastore.Key(sourceURL, extFromContentType(contentType))
				if derr := d.Store.Delete(ctx, key); derr != nil &&
					!errors.Is(derr, mediastore.ErrNotFound) &&
					!errors.Is(derr, mediastore.ErrNotSupported) {
					res.StoreFailures++
					log.WarnContext(ctx, "media_sweep.store_delete_failed",
						slog.String("hash", hash),
						slog.String("key", key),
						slog.String("error", derr.Error()))
					return nil
				}
			}
			if derr := d.Assets.DeleteByHash(ctx, hash); derr != nil {
				log.WarnContext(ctx, "media_sweep.row_delete_failed",
					slog.String("hash", hash),
					slog.String("error", derr.Error()))
				return nil
			}
			res.Deleted++
			return nil
		})
		if err != nil {
			return res, fmt.Errorf("iterate cold assets: %w", err)
		}
		return res, nil
	}
}

// extFromContentType maps a media content-type to a file extension —
// mirrors the (unexported) handlers helper so this package stays
// import-clean.
func extFromContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return ""
}
