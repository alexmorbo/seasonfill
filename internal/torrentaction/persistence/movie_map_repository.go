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

// MovieMapRepository is the movie twin of SeriesMapRepository
// (ADR-0023 B1.1): the fallback the torrentaction guard consults when a
// movie torrent hash has no grab_records row. Torrents seasonfill only
// OBSERVES (Radarr-driven downloads bridged into torrent_movie_map by
// the webhook + reconciler) never produced a grab_record, so the grab
// guard would 404 every displayed movie torrent. FindByHash recovers
// the owning instance from the same bridge table the display path reads.
//
// Stateless GORM wrapper — same shape as SeriesMapRepository; honours an
// ambient transaction via dbtx.DBFromContext. Satisfies app.MovieMap.
type MovieMapRepository struct {
	db *gorm.DB
}

// NewMovieMapRepository constructs the repository over the shared *gorm.DB.
func NewMovieMapRepository(db *gorm.DB) *MovieMapRepository {
	return &MovieMapRepository{db: db}
}

// FindByHash resolves the owning instance for a torrent hash from
// torrent_movie_map. hash is lowercased+trimmed to match how the sync /
// reconciler paths store it (the guard already normalises, this is
// belt-and-braces). Empty hash and no-row both return the shared 404
// shape (GrabNotFoundError + ports.ErrNotFound) so the handler maps them
// to 404, identical to the grab guard's miss.
//
// (instance_name, torrent_hash) is the composite PK, so a hash MAY map
// to more than one instance; we pick deterministically (instance_name
// ASC, LIMIT 1), mirroring the series guard's resolution.
func (r *MovieMapRepository) FindByHash(ctx context.Context, hash string) (appta.MovieMapRef, error) {
	h := strings.ToLower(strings.TrimSpace(hash))
	if h == "" {
		return appta.MovieMapRef{}, errors.Join(
			&sharedErrors.GrabNotFoundError{ID: "hash:" + h},
			ports.ErrNotFound,
		)
	}
	db := dbtx.DBFromContext(ctx, r.db).WithContext(ctx)

	var m database.TorrentMovieMapModel
	err := db.Model(&database.TorrentMovieMapModel{}).
		Where("torrent_hash = ?", h).
		Order("instance_name ASC").
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appta.MovieMapRef{}, errors.Join(
				&sharedErrors.GrabNotFoundError{ID: "hash:" + h},
				ports.ErrNotFound,
			)
		}
		return appta.MovieMapRef{}, fmt.Errorf("find torrent_movie_map by hash: %w", err)
	}
	return appta.MovieMapRef{
		InstanceName:  m.InstanceName,
		RadarrMovieID: m.RadarrMovieID,
	}, nil
}

// Compile-time port check.
var _ appta.MovieMap = (*MovieMapRepository)(nil)
