package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	reqapp "github.com/alexmorbo/seasonfill/internal/request/app"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// RequestRepository is the GORM-backed requests store.
type RequestRepository struct{ db *gorm.DB }

// NewRequestRepository constructs a RequestRepository bound to db.
func NewRequestRepository(db *gorm.DB) *RequestRepository {
	return &RequestRepository{db: db}
}

// InsertPending returns the existing pending row when one matches
// (user_id, media_type, tmdb_id), else inserts a new pending row. The
// SELECT-first path keeps idempotency dialect-portable; the partial-unique
// index is the concurrency backstop.
func (r *RequestRepository) InsertPending(ctx context.Context, req reqdomain.Request) (int64, bool, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var existing database.RequestModel
	err := db.Where("user_id = ? AND media_type = ? AND tmdb_id = ? AND status = ?",
		req.UserID, req.MediaType, req.TMDBID, reqdomain.StatusPending).
		First(&existing).Error
	if err == nil {
		return int64(existing.ID), true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, fmt.Errorf("select pending request: %w", err)
	}

	m, mErr := domainToModel(req)
	if mErr != nil {
		return 0, false, mErr
	}
	if cErr := db.Create(&m).Error; cErr != nil {
		// Concurrency backstop: partial-unique violation → reselect.
		var again database.RequestModel
		if sErr := db.Where("user_id = ? AND media_type = ? AND tmdb_id = ? AND status = ?",
			req.UserID, req.MediaType, req.TMDBID, reqdomain.StatusPending).
			First(&again).Error; sErr == nil {
			return int64(again.ID), true, nil
		}
		return 0, false, fmt.Errorf("insert pending request: %w", cErr)
	}
	return int64(m.ID), false, nil
}

// Get returns the request by id; ports.ErrNotFound on miss.
func (r *RequestRepository) Get(ctx context.Context, id int64) (reqdomain.Request, error) {
	var m database.RequestModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).First(&m, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return reqdomain.Request{}, ports.ErrNotFound
		}
		return reqdomain.Request{}, fmt.Errorf("get request %d: %w", id, err)
	}
	return modelToDomain(m)
}

// ListByUser returns the caller's requests, newest first.
func (r *RequestRepository) ListByUser(ctx context.Context, userID uint) ([]reqdomain.Request, error) {
	return r.list(ctx, r.db.WithContext(ctx).Where("user_id = ?", userID))
}

// ListAll returns every request, newest first.
func (r *RequestRepository) ListAll(ctx context.Context) ([]reqdomain.Request, error) {
	return r.list(ctx, r.db.WithContext(ctx))
}

func (r *RequestRepository) list(ctx context.Context, q *gorm.DB) ([]reqdomain.Request, error) {
	var ms []database.RequestModel
	if err := q.Order("created_at DESC").Find(&ms).Error; err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}
	out := make([]reqdomain.Request, 0, len(ms))
	for _, m := range ms {
		d, err := modelToDomain(m)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// SetStatus flips status + approver_id + updated_at on the tx-scoped DB.
func (r *RequestRepository) SetStatus(ctx context.Context, id int64, status string, approverID uint) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.RequestModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      status,
			"approver_id": approverID,
			"updated_at":  gorm.Expr("CURRENT_TIMESTAMP"),
		})
	if res.Error != nil {
		return fmt.Errorf("set request %d status: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func domainToModel(r reqdomain.Request) (database.RequestModel, error) {
	payload, err := json.Marshal(r.Spec)
	if err != nil {
		return database.RequestModel{}, fmt.Errorf("marshal spec: %w", err)
	}
	m := database.RequestModel{
		UserID:    r.UserID,
		MediaType: r.MediaType,
		TMDBID:    r.TMDBID,
		Payload:   datatypes.JSON(payload),
		Status:    r.Status,
	}
	if r.Seasons != nil {
		raw, sErr := json.Marshal(*r.Seasons)
		if sErr != nil {
			return database.RequestModel{}, fmt.Errorf("marshal seasons: %w", sErr)
		}
		m.Seasons = datatypes.JSON(raw)
	}
	return m, nil
}

func modelToDomain(m database.RequestModel) (reqdomain.Request, error) {
	var spec reqdomain.AddSpec
	if len(m.Payload) > 0 {
		if err := json.Unmarshal(m.Payload, &spec); err != nil {
			return reqdomain.Request{}, fmt.Errorf("unmarshal payload: %w", err)
		}
	}
	var seasons *[]int
	if len(m.Seasons) > 0 {
		var s []int
		if err := json.Unmarshal(m.Seasons, &s); err != nil {
			return reqdomain.Request{}, fmt.Errorf("unmarshal seasons: %w", err)
		}
		seasons = &s
	}
	return reqdomain.Request{
		ID:         m.ID,
		UserID:     m.UserID,
		MediaType:  m.MediaType,
		TMDBID:     m.TMDBID,
		Seasons:    seasons,
		Spec:       spec,
		Status:     m.Status,
		ApproverID: m.ApproverID,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}, nil
}

var _ reqapp.RequestRepository = (*RequestRepository)(nil)
