package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// EpisodeGrabRepository persists episode_grabs rows. 467a / D-6.
type EpisodeGrabRepository struct {
	db *gorm.DB
}

// NewEpisodeGrabRepository wires the repository to a GORM DB.
func NewEpisodeGrabRepository(db *gorm.DB) *EpisodeGrabRepository {
	return &EpisodeGrabRepository{db: db}
}

// BatchUpsert inserts (grab_id, episode_id, episode_number) triples in a
// single INSERT ... ON CONFLICT (grab_id, episode_id) DO UPDATE
// updated_at = now() round-trip. Empty input returns nil with no SQL.
//
// The composite (grab_id, episode_id) PK is the only natural key — a
// grab covering 10 episodes always emits 10 rows; the OnConflict
// updated_at bump records the re-delivery without inserting duplicates.
func (r *EpisodeGrabRepository) BatchUpsert(ctx context.Context, refs []grab.EpisodeRef) error {
	if len(refs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]database.EpisodeGrabModel, 0, len(refs))
	for _, ref := range refs {
		created := ref.CreatedAt
		if created.IsZero() {
			created = now
		}
		rows = append(rows, database.EpisodeGrabModel{
			GrabID:        ref.GrabID,
			EpisodeID:     int64(ref.EpisodeID),
			EpisodeNumber: ref.EpisodeNumber,
			CreatedAt:     created,
			UpdatedAt:     now,
		})
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "grab_id"}, {Name: "episode_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"updated_at": now,
		}),
	}).Create(&rows)
	if res.Error != nil {
		return fmt.Errorf("batch upsert episode_grabs: %w", res.Error)
	}
	return nil
}

// ListByGrabID returns every episode ref pinned to the supplied grab id,
// ordered by episode_number ASC. Empty result with no error if the grab
// has no episode fanout.
func (r *EpisodeGrabRepository) ListByGrabID(ctx context.Context, grabID string) ([]grab.EpisodeRef, error) {
	var models []database.EpisodeGrabModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("grab_id = ?", grabID).
		Order("episode_number ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list episode_grabs by grab_id: %w", err)
	}
	out := make([]grab.EpisodeRef, 0, len(models))
	for _, m := range models {
		out = append(out, toEpisodeRef(m))
	}
	return out, nil
}

// ListByEpisodeID returns every grab that referenced the supplied
// episode, ordered by created_at DESC.
func (r *EpisodeGrabRepository) ListByEpisodeID(ctx context.Context, episodeID domain.EpisodeID) ([]grab.EpisodeRef, error) {
	var models []database.EpisodeGrabModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("episode_id = ?", int64(episodeID)).
		Order("created_at DESC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list episode_grabs by episode_id: %w", err)
	}
	out := make([]grab.EpisodeRef, 0, len(models))
	for _, m := range models {
		out = append(out, toEpisodeRef(m))
	}
	return out, nil
}

func toEpisodeRef(m database.EpisodeGrabModel) grab.EpisodeRef {
	return grab.EpisodeRef{
		GrabID:        m.GrabID,
		EpisodeID:     domain.EpisodeID(m.EpisodeID),
		EpisodeNumber: m.EpisodeNumber,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

var _ ports.EpisodeGrabRepository = (*EpisodeGrabRepository)(nil)
