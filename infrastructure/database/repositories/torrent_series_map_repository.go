package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

// TorrentSeriesMapRepository persists the torrent_series_map table
// (migration 000035, PRD §4.5 + §7.3). Implements
// application/torrentsync.MapRepo. First-source-wins: ON CONFLICT
// updates only `created_at` (touch — useful for debugging when an
// existing row gets re-touched) — `series_id`, `season_number`,
// and `source` stay stuck to the row's original insert. That
// matches story 221's design: source priority is webhook >
// grab_record > sonarr_queue > sonarr_history, and once a row is
// in we trust the first-source-to-win.
type TorrentSeriesMapRepository struct {
	db    *gorm.DB
	clock func() time.Time
}

// NewTorrentSeriesMapRepository wires the repo.
func NewTorrentSeriesMapRepository(db *gorm.DB) *TorrentSeriesMapRepository {
	return &TorrentSeriesMapRepository{
		db:    db,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// Upsert is the non-tx entrypoint used by the reconciler. Routes
// the write through dbFromContext so callers inside a tx pick up
// the tx scope automatically.
func (r *TorrentSeriesMapRepository) Upsert(ctx context.Context, row torrentsync.MapRow) error {
	return r.upsert(ctx, row)
}

// UpsertTx is the tx-scoped entrypoint used by the webhook path.
// The supplied ctx MUST carry a tx scope (Transactor.Transaction).
// Identical body to Upsert — kept as a separate method so the
// intent at the call site is explicit.
func (r *TorrentSeriesMapRepository) UpsertTx(ctx context.Context, row torrentsync.MapRow) error {
	return r.upsert(ctx, row)
}

func (r *TorrentSeriesMapRepository) upsert(ctx context.Context, row torrentsync.MapRow) error {
	if row.Instance == "" || row.Hash == "" {
		return fmt.Errorf("torrent_series_map upsert: empty instance or hash")
	}
	if row.SeriesID <= 0 {
		return fmt.Errorf("torrent_series_map upsert: missing series_id")
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = r.clock()
	}
	model := database.TorrentSeriesMapModel{
		InstanceName: row.Instance,
		TorrentHash:  row.Hash,
		SeriesID:     row.SeriesID,
		Source:       string(row.Source),
		CreatedAt:    row.CreatedAt,
	}
	if row.SeasonNumber > 0 {
		v := row.SeasonNumber
		model.SeasonNumber = &v
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "instance_name"}, {Name: "torrent_hash"}},
		DoUpdates: clause.AssignmentColumns([]string{"created_at"}),
	}).Create(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Not actually possible on Create but mirrors the
			// defence pattern in the qbit_torrents repo.
			return nil
		}
		return fmt.Errorf("upsert torrent_series_map: %w", err)
	}
	return nil
}

// Compile-time port check.
var _ torrentsync.MapRepo = (*TorrentSeriesMapRepository)(nil)
