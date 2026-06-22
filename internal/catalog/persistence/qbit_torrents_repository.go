package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// QbitTorrentsRepository persists the qbit_torrents table
// (migration 000035, PRD §7.3). Implements
// application/torrentsync.TorrentsRepo. The repository owns the
// transaction boundary for BatchUpsert — one tx per call,
// regardless of batch size — per PRD §13 risk 2 (sqlite
// contention with C-2's transactional enrichment upserts).
type QbitTorrentsRepository struct {
	db    *gorm.DB
	clock func() time.Time
}

// NewQbitTorrentsRepository wires the repo bound to db.
func NewQbitTorrentsRepository(db *gorm.DB) *QbitTorrentsRepository {
	return &QbitTorrentsRepository{
		db:    db,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// Upsert writes one (instance, hash) row. ON CONFLICT updates
// every non-PK column except `first_seen_at` (sticky to the
// first insert) and `present` (reset to true so an upsert
// re-incarnates a previously-deleted hash — cross-seed scenario).
func (r *QbitTorrentsRepository) Upsert(ctx context.Context, instance domain.InstanceName, e torrentsync.Entry) error {
	model := modelFromEntry(instance, e, r.clock())
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "instance_name"}, {Name: "hash"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"infohash_v2", "name", "category", "tags", "tracker_host",
			"save_path", "content_path", "state_raw", "state_group",
			"size_bytes", "total_size", "downloaded", "uploaded",
			"ratio", "popularity", "time_active_s", "seeding_time_s",
			"added_on", "completion_on", "last_activity",
			"season_number",
			"present", "deleted_at", "updated_at",
		}),
	}).Create(&model).Error
	if err != nil {
		return fmt.Errorf("upsert qbit_torrent: %w", err)
	}
	return nil
}

// BatchUpsert writes the supplied entries inside one transaction.
// On any per-row error the tx rolls back — callers re-queue the
// pending set for retry on the next flush window.
func (r *QbitTorrentsRepository) BatchUpsert(ctx context.Context, instance domain.InstanceName, entries []torrentsync.Entry, updatedAt time.Time) error {
	if len(entries) == 0 {
		return nil
	}
	if updatedAt.IsZero() {
		updatedAt = r.clock()
	}
	models := make([]database.QbitTorrentModel, 0, len(entries))
	for _, e := range entries {
		models = append(models, modelFromEntry(instance, e, updatedAt))
	}
	return dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "instance_name"}, {Name: "hash"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"infohash_v2", "name", "category", "tags", "tracker_host",
				"save_path", "content_path", "state_raw", "state_group",
				"size_bytes", "total_size", "downloaded", "uploaded",
				"ratio", "popularity", "time_active_s", "seeding_time_s",
				"added_on", "completion_on", "last_activity",
				"season_number",
				"present", "deleted_at", "updated_at",
			}),
		}).CreateInBatches(models, 100).Error
		if err != nil {
			return fmt.Errorf("batch upsert qbit_torrents: %w", err)
		}
		return nil
	})
}

// MarkAbsent flips present=false + deleted_at=when. Returns nil
// when the row does not exist — removal of a hash we never
// persisted is a no-op.
func (r *QbitTorrentsRepository) MarkAbsent(ctx context.Context, instance domain.InstanceName, hash string, when time.Time) error {
	if when.IsZero() {
		when = r.clock()
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.QbitTorrentModel{}).
		Where("instance_name = ? AND hash = ?", instance, hash).
		Updates(map[string]any{
			"present":    false,
			"deleted_at": when,
			"updated_at": when,
		})
	if res.Error != nil {
		return fmt.Errorf("mark qbit_torrent absent: %w", res.Error)
	}
	return nil
}

// List returns every row for the instance with present=true. The
// loop's restart recovery uses this to repopulate the memory
// store; absent rows are excluded because the read endpoint never
// surfaces them.
func (r *QbitTorrentsRepository) List(ctx context.Context, instance domain.InstanceName) ([]torrentsync.Entry, error) {
	var models []database.QbitTorrentModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND present = ?", instance, true).
		Find(&models).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("list qbit_torrents: %w", err)
	}
	out := make([]torrentsync.Entry, 0, len(models))
	for _, m := range models {
		out = append(out, entryFromModel(m))
	}
	return out, nil
}

// FindByHashes returns the qbit_torrents rows matching every
// (instance, hash) tuple in `hashes`. Unlike List it includes
// present=false rows — the read endpoint (story 222) MUST
// surface deleted-but-known torrents so the UI can render
// historical inventory when qBit is unreachable.
//
// Empty input returns nil, nil (no round-trip). The returned
// Entry's live fields are zero by construction — the schema
// does not persist them.
func (r *QbitTorrentsRepository) FindByHashes(ctx context.Context, instance domain.InstanceName, hashes []string) ([]torrentsync.Entry, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	var models []database.QbitTorrentModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND hash IN ?", instance, hashes).
		Find(&models).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("find qbit_torrents by hashes: %w", err)
	}
	out := make([]torrentsync.Entry, 0, len(models))
	for _, m := range models {
		out = append(out, entryFromModel(m))
	}
	return out, nil
}

// CountPresentByInstance returns the number of `present=true` rows in
// qbit_torrents for the instance. Used by the periodic capacity
// collector (cmd/server/loops/qbit_capacity.go) to feed the
// seasonfill_qbit_torrents_rows gauge. Returns (0, nil) when the
// instance has never been seen.
func (r *QbitTorrentsRepository) CountPresentByInstance(ctx context.Context, instance domain.InstanceName) (int, error) {
	var count int64
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.QbitTorrentModel{}).
		Where("instance_name = ? AND present = ?", instance, true).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count present qbit_torrents: %w", err)
	}
	return int(count), nil
}

// modelFromEntry projects a torrentsync.Entry into the GORM
// model. Live fields are NOT carried over — they have no column.
func modelFromEntry(instance domain.InstanceName, e torrentsync.Entry, updatedAt time.Time) database.QbitTorrentModel {
	info := e.Info
	m := database.QbitTorrentModel{
		InstanceName: instance,
		Hash:         info.Hash,
		Name:         info.Name,
		StateRaw:     info.StateRaw,
		StateGroup:   string(e.StateGroup),
		SizeBytes:    info.Size,
		TotalSize:    info.TotalSize,
		Downloaded:   info.Downloaded,
		Uploaded:     info.Uploaded,
		Ratio:        info.Ratio,
		Popularity:   info.Popularity,
		TimeActiveS:  int64(info.TimeActive / time.Second),
		SeedingTimeS: int64(info.SeedingTime / time.Second),
		Present:      true,
		FirstSeenAt:  e.SyncedAt,
		UpdatedAt:    updatedAt,
	}
	if info.InfohashV2 != "" {
		v := info.InfohashV2
		m.InfohashV2 = &v
	}
	if info.Category != "" {
		v := info.Category
		m.Category = &v
	}
	if info.Tags != "" {
		v := info.Tags
		m.Tags = &v
	}
	if info.TrackerHost != "" {
		v := info.TrackerHost
		m.TrackerHost = &v
	}
	if info.SavePath != "" {
		v := info.SavePath
		m.SavePath = &v
	}
	if info.ContentPath != "" {
		v := info.ContentPath
		m.ContentPath = &v
	}
	if !info.AddedOn.IsZero() {
		t := info.AddedOn
		m.AddedOn = &t
	}
	if !info.CompletionOn.IsZero() {
		t := info.CompletionOn
		m.CompletionOn = &t
	}
	if !info.LastActivity.IsZero() {
		t := info.LastActivity
		m.LastActivity = &t
	}
	if info.SeasonNumber != nil {
		v := *info.SeasonNumber
		m.SeasonNumber = &v
	}
	return m
}

// entryFromModel projects the GORM model back into the
// torrentsync.Entry the use case consumes. Used by List during
// restart recovery; live fields stay zero as documented.
func entryFromModel(m database.QbitTorrentModel) torrentsync.Entry {
	info := qbit.TorrentInfo{
		Hash:        m.Hash,
		Name:        m.Name,
		StateRaw:    m.StateRaw,
		StateGroup:  qbit.StateGroup(m.StateGroup),
		Size:        m.SizeBytes,
		TotalSize:   m.TotalSize,
		Downloaded:  m.Downloaded,
		Uploaded:    m.Uploaded,
		Ratio:       m.Ratio,
		Popularity:  m.Popularity,
		TimeActive:  time.Duration(m.TimeActiveS) * time.Second,
		SeedingTime: time.Duration(m.SeedingTimeS) * time.Second,
	}
	if m.InfohashV2 != nil {
		info.InfohashV2 = *m.InfohashV2
	}
	if m.Category != nil {
		info.Category = *m.Category
	}
	if m.Tags != nil {
		info.Tags = *m.Tags
	}
	if m.TrackerHost != nil {
		info.TrackerHost = *m.TrackerHost
	}
	if m.SavePath != nil {
		info.SavePath = *m.SavePath
	}
	if m.ContentPath != nil {
		info.ContentPath = *m.ContentPath
	}
	if m.AddedOn != nil {
		info.AddedOn = *m.AddedOn
	}
	if m.CompletionOn != nil {
		info.CompletionOn = *m.CompletionOn
	}
	if m.LastActivity != nil {
		info.LastActivity = *m.LastActivity
	}
	if m.SeasonNumber != nil {
		v := *m.SeasonNumber
		info.SeasonNumber = &v
	}
	return torrentsync.Entry{
		Info:                info,
		StateGroup:          qbit.StateGroup(m.StateGroup),
		SyncedAt:            m.UpdatedAt,
		LastFlushedCounters: torrentsync.CountersFrom(info),
	}
}

// Compile-time port check.
var _ torrentsync.TorrentsRepo = (*QbitTorrentsRepository)(nil)
