package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	appta "github.com/alexmorbo/seasonfill/internal/torrentaction/app"
)

// SeriesMapRepository is the ADR-0013 Q5 fallback the torrentaction guard
// consults when a hash has no grab_records row. Torrents seasonfill only
// OBSERVES (Sonarr-driven downloads bridged into torrent_series_map by the
// reconciler + webhook) never produced a grab_record, so the grab guard
// 404s every displayed torrent. FindByHash recovers the owning instance
// from the same bridge table the display path reads.
//
// Stateless GORM wrapper — same shape as AuditRepository in this package;
// honours an ambient transaction via dbtx.DBFromContext. Satisfies
// app.SeriesMap.
type SeriesMapRepository struct {
	db *gorm.DB
}

// NewSeriesMapRepository constructs the repository over the shared *gorm.DB.
func NewSeriesMapRepository(db *gorm.DB) *SeriesMapRepository {
	return &SeriesMapRepository{db: db}
}

// FindByHash resolves the owning instance for a torrent hash from
// torrent_series_map. hash is lowercased+trimmed to match how the sync /
// reconciler paths store it (the guard already normalises, this is
// belt-and-braces). Empty hash and no-row both return the shared 404 shape
// (GrabNotFoundError + ports.ErrNotFound) so the handler maps them to 404,
// identical to the grab guard's miss.
//
// (instance_name, torrent_hash) is the composite PK, so a hash MAY map to
// more than one instance; we pick deterministically (instance_name ASC,
// LIMIT 1), mirroring the grab guard's single-instance resolution.
func (r *SeriesMapRepository) FindByHash(ctx context.Context, hash string) (appta.SeriesMapRef, error) {
	h := strings.ToLower(strings.TrimSpace(hash))
	if h == "" {
		return appta.SeriesMapRef{}, errors.Join(
			&sharedErrors.GrabNotFoundError{ID: "hash:" + h},
			ports.ErrNotFound,
		)
	}
	db := dbtx.DBFromContext(ctx, r.db).WithContext(ctx)

	var m database.TorrentSeriesMapModel
	err := db.Model(&database.TorrentSeriesMapModel{}).
		Where("torrent_hash = ?", h).
		Order("instance_name ASC").
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appta.SeriesMapRef{}, errors.Join(
				&sharedErrors.GrabNotFoundError{ID: "hash:" + h},
				ports.ErrNotFound,
			)
		}
		return appta.SeriesMapRef{}, fmt.Errorf("find torrent_series_map by hash: %w", err)
	}
	return appta.SeriesMapRef{
		InstanceName: m.InstanceName,
		SeriesID:     m.SeriesID,
	}, nil
}

// Compile-time port check.
var _ appta.SeriesMap = (*SeriesMapRepository)(nil)
