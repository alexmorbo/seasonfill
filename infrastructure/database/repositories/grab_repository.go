package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

type GrabRepository struct {
	db *gorm.DB
}

func NewGrabRepository(db *gorm.DB) *GrabRepository {
	return &GrabRepository{db: db}
}

// ErrGrabDuplicate signals the (instance, series, season, release_guid)
// unique index trapped a duplicate INSERT. Race-recovery path in the
// HTTP grab handler resolves the survivor via FindExisting4Tuple.
var ErrGrabDuplicate = errors.New("grab record already exists for 4-tuple")

func (r *GrabRepository) Create(ctx context.Context, rec grab.Record) error {
	model := toGrabModel(rec)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&model).Error
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: %s/%d/%d/%s", ErrGrabDuplicate,
			rec.InstanceName, rec.SeriesID, rec.SeasonNumber, rec.ReleaseGUID)
	}
	return fmt.Errorf("create grab_record: %w", err)
}

// FindExisting4Tuple resolves the surviving row after a duplicate-key INSERT
// trips the unique index. Returns ports.ErrNotFound when no row matches.
func (r *GrabRepository) FindExisting4Tuple(ctx context.Context, instance string,
	seriesID, season int, guid string) (grab.Record, error) {
	var m database.GrabRecordModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND series_id = ? AND season_number = ? AND release_guid = ?",
			instance, seriesID, season, guid).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.Record{}, ports.ErrNotFound
		}
		return grab.Record{}, fmt.Errorf("find grab by 4-tuple: %w", err)
	}
	return toGrabRecord(m)
}

func toGrabModel(r grab.Record) database.GrabRecordModel {
	return database.GrabRecordModel{
		ID:                r.ID.String(),
		InstanceName:      r.InstanceName,
		SeriesID:          r.SeriesID,
		SeriesTitle:       r.SeriesTitle,
		SeasonNumber:      r.SeasonNumber,
		ReleaseGUID:       r.ReleaseGUID,
		ReleaseTitle:      r.ReleaseTitle,
		DownloadID:        r.DownloadID,
		IndexerID:         r.IndexerID,
		IndexerName:       r.IndexerName,
		CustomFormatScore: r.CustomFormatScore,
		Quality:           r.Quality,
		CoverageCount:     r.CoverageCount,
		Status:            string(r.Status),
		ErrorMessage:      r.ErrorMessage,
		ScanRunID:         r.ScanRunID.String(),
		Attempts:          r.Attempts,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

func (r *GrabRepository) List(ctx context.Context, f ports.GrabFilter, p ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	if p.Limit <= 0 || p.Limit > ports.MaxListLimit {
		return nil, nil, fmt.Errorf("grab list: %w", ports.ErrInvalidLimit)
	}
	q := dbFromContext(ctx, r.db).WithContext(ctx).Model(&database.GrabRecordModel{})
	if f.Instance != nil {
		q = q.Where("instance_name = ?", *f.Instance)
	}
	if f.SeriesID != nil {
		q = q.Where("series_id = ?", *f.SeriesID)
	}
	if f.SeasonNumber != nil {
		q = q.Where("season_number = ?", *f.SeasonNumber)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at < ?", *f.To)
	}
	if p.Cursor != nil {
		q = q.Where("(created_at, id) < (?, ?)", p.Cursor.Timestamp, p.Cursor.ID)
	}
	var models []database.GrabRecordModel
	if err := q.Order("created_at DESC, id DESC").Limit(p.Limit + 1).Find(&models).Error; err != nil {
		return nil, nil, fmt.Errorf("grab list: %w", err)
	}
	var next *ports.Cursor
	if len(models) > p.Limit {
		last := models[p.Limit-1]
		next = &ports.Cursor{Timestamp: last.CreatedAt.UTC(), ID: last.ID}
		models = models[:p.Limit]
	}
	out := make([]grab.Record, 0, len(models))
	for _, m := range models {
		rec, err := toGrabRecord(m)
		if err != nil {
			return nil, nil, fmt.Errorf("grab list: %w", err)
		}
		out = append(out, rec)
	}
	return out, next, nil
}

func toGrabRecord(m database.GrabRecordModel) (grab.Record, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil {
		return grab.Record{}, fmt.Errorf("parse grab id: %w", err)
	}
	scanRunID, err := uuid.Parse(m.ScanRunID)
	if err != nil {
		return grab.Record{}, fmt.Errorf("parse scan_run_id: %w", err)
	}
	return grab.Record{
		ID:                id,
		InstanceName:      m.InstanceName,
		SeriesID:          m.SeriesID,
		SeriesTitle:       m.SeriesTitle,
		SeasonNumber:      m.SeasonNumber,
		ReleaseGUID:       m.ReleaseGUID,
		ReleaseTitle:      m.ReleaseTitle,
		DownloadID:        m.DownloadID,
		IndexerID:         m.IndexerID,
		IndexerName:       m.IndexerName,
		CustomFormatScore: m.CustomFormatScore,
		Quality:           m.Quality,
		CoverageCount:     m.CoverageCount,
		Status:            grab.Status(m.Status),
		ErrorMessage:      m.ErrorMessage,
		ScanRunID:         scanRunID,
		Attempts:          m.Attempts,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}, nil
}

// MatchLatest implements the (downloadId-first, fallback-tuple) lookup
// described on ports.MatchKey. Two sequential SELECTs: the primary path
// short-circuits as soon as DownloadID resolves a non-terminal row; the
// fallback runs only when DownloadID is empty OR the primary missed.
func (r *GrabRepository) MatchLatest(ctx context.Context, key ports.MatchKey) (grab.Record, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	terminal := []string{
		string(grab.StatusImported),
		string(grab.StatusImportFailed),
		string(grab.StatusGrabFailed),
	}

	if key.DownloadID != "" {
		var m database.GrabRecordModel
		err := db.Model(&database.GrabRecordModel{}).
			Where("download_id = ? AND instance_name = ? AND status NOT IN ?",
				key.DownloadID, key.InstanceName, terminal).
			Order("created_at DESC, id DESC").
			First(&m).Error
		if err == nil {
			rec, convErr := toGrabRecord(m)
			if convErr != nil {
				return grab.Record{}, fmt.Errorf("match latest: %w", convErr)
			}
			return rec, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.Record{}, fmt.Errorf("match latest by download_id: %w", err)
		}
		// fall through to fallback
	}

	if key.ReleaseTitle == "" || key.InstanceName == "" {
		return grab.Record{}, ports.ErrNotFound
	}

	var m database.GrabRecordModel
	err := db.Model(&database.GrabRecordModel{}).
		Where("release_title = ? AND series_id = ? AND season_number = ? AND instance_name = ? AND status NOT IN ?",
			key.ReleaseTitle, key.SeriesID, key.SeasonNumber, key.InstanceName, terminal).
		Order("created_at DESC, id DESC").
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.Record{}, ports.ErrNotFound
		}
		return grab.Record{}, fmt.Errorf("match latest by tuple: %w", err)
	}
	rec, convErr := toGrabRecord(m)
	if convErr != nil {
		return grab.Record{}, fmt.Errorf("match latest: %w", convErr)
	}
	return rec, nil
}

// UpdateStatus writes status + error_message + updated_at. Defence-in-depth
// guard: reads current status and rejects the write via
// grab.ErrInvalidStatusTransition when the move is forbidden.
func (r *GrabRepository) UpdateStatus(ctx context.Context, id uuid.UUID, newStatus grab.Status, message string) error {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var current database.GrabRecordModel
	if err := db.Select("id", "status").First(&current, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("read grab status: %w", err)
	}

	if !grab.Status(current.Status).CanTransitionTo(newStatus) {
		return fmt.Errorf("%w: %q -> %q",
			grab.ErrInvalidStatusTransition, current.Status, string(newStatus))
	}

	now := time.Now().UTC()
	res := db.Model(&database.GrabRecordModel{}).
		Where("id = ?", id.String()).
		Updates(map[string]any{
			"status":        string(newStatus),
			"error_message": message,
			"updated_at":    now,
		})
	if res.Error != nil {
		return fmt.Errorf("update grab status: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// Ensure interface compliance at compile time.
var _ ports.GrabRepository = (*GrabRepository)(nil)
