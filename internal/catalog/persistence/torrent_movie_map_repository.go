package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TorrentMovieMapRepository persists the torrent_movie_map table
// (migration 000065, ADR-0023 B1.1) — the movie twin of
// TorrentSeriesMapRepository. Implements torrentsync.MovieMapRepo and
// torrentsync.MovieLookupRepo.
//
// First-source-wins: ON CONFLICT updates only `created_at` (touch —
// useful when debugging a re-touched row); radarr_movie_id, source and
// provenance stay stuck to the row's original insert. Source priority
// for movies is webhook > radarr_queue > radarr_history, and once a row
// is in we trust the first source to have won.
//
// source and provenance carry no DB CHECK, so the repo refuses empty
// strings for both — a NOT NULL text column would happily swallow ""
// and poison the enum. (This is the one behavioural addition over the
// series repo, which has no such column.)
type TorrentMovieMapRepository struct {
	db    *gorm.DB
	clock func() time.Time
}

// NewTorrentMovieMapRepository wires the repo.
func NewTorrentMovieMapRepository(db *gorm.DB) *TorrentMovieMapRepository {
	return &TorrentMovieMapRepository{
		db:    db,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// Upsert is the non-tx entrypoint (reconciler path, B1.3). Routes the
// write through dbFromContext so callers already inside a tx pick up
// the tx scope automatically.
func (r *TorrentMovieMapRepository) Upsert(ctx context.Context, row torrentsync.MovieMapRow) error {
	return r.upsert(ctx, row)
}

// UpsertTx is the tx-scoped entrypoint (webhook path, B1.2). The
// supplied ctx MUST carry a tx scope (Transactor.Transaction).
// Identical body to Upsert — kept separate so the intent at the call
// site is explicit.
func (r *TorrentMovieMapRepository) UpsertTx(ctx context.Context, row torrentsync.MovieMapRow) error {
	return r.upsert(ctx, row)
}

func (r *TorrentMovieMapRepository) upsert(ctx context.Context, row torrentsync.MovieMapRow) error {
	if row.Instance == "" || row.Hash == "" {
		return fmt.Errorf("torrent_movie_map upsert: empty instance or hash")
	}
	if row.RadarrMovieID <= 0 {
		return fmt.Errorf("torrent_movie_map upsert: missing radarr_movie_id")
	}
	if row.Source == "" {
		return fmt.Errorf("torrent_movie_map upsert: empty source")
	}
	if row.Provenance == "" {
		return fmt.Errorf("torrent_movie_map upsert: empty provenance")
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = r.clock()
	}
	model := database.TorrentMovieMapModel{
		InstanceName:  row.Instance,
		TorrentHash:   domain.QbitHash(row.Hash),
		RadarrMovieID: row.RadarrMovieID,
		Source:        string(row.Source),
		Provenance:    string(row.Provenance),
		CreatedAt:     row.CreatedAt,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "instance_name"}, {Name: "torrent_hash"}},
		DoUpdates: clause.AssignmentColumns([]string{"created_at"}),
	}).Create(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Not actually possible on Create but mirrors the
			// defence pattern in the series/qbit_torrents repos.
			return nil
		}
		return fmt.Errorf("upsert torrent_movie_map: %w", err)
	}
	return nil
}

// HashesForMovie returns every torrent_hash mapped to
// (instance, radarr_movie_id) regardless of source. Empty result on no
// rows. Twin of TorrentSeriesMapRepository.HashesForSeries; implements
// torrentsync.MovieLookupRepo.
func (r *TorrentMovieMapRepository) HashesForMovie(ctx context.Context, instance domain.InstanceName, radarrMovieID domain.RadarrMovieID) ([]string, error) {
	var rows []database.TorrentMovieMapModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Select("torrent_hash").
		Where("instance_name = ? AND radarr_movie_id = ?", instance, radarrMovieID).
		Find(&rows).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("hashes for movie: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, m := range rows {
		if m.TorrentHash != "" {
			out = append(out, string(m.TorrentHash))
		}
	}
	return out, nil
}

// Compile-time port checks.
var _ torrentsync.MovieMapRepo = (*TorrentMovieMapRepository)(nil)
var _ torrentsync.MovieLookupRepo = (*TorrentMovieMapRepository)(nil)
