// blocklist_repository.go persists ADR-0017 Ф5 S3 discovery_blocklist.
// CRUD + the two accessors the in-memory BlocklistCache needs
// (LoadBlockSets) and the handler's resolved read (ListResolved).
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
)

// discoveryBlocklistModel is the GORM row for discovery_blocklist.
type discoveryBlocklistModel struct {
	ID        int64   `gorm:"column:id;primaryKey;autoIncrement"`
	Kind      string  `gorm:"column:kind"`
	RefID     int64   `gorm:"column:ref_id"`
	Label     *string `gorm:"column:label"`
	CreatedAt int64   `gorm:"column:created_at;autoCreateTime:false;->"`
}

func (discoveryBlocklistModel) TableName() string { return "discovery_blocklist" }

// BlocklistRepository reads/writes discovery_blocklist.
type BlocklistRepository struct {
	db *gorm.DB
}

func NewBlocklistRepository(db *gorm.DB) *BlocklistRepository {
	if db == nil {
		panic("discovery blocklist repo: db required")
	}
	return &BlocklistRepository{db: db}
}

// Insert adds (kind, ref_id, label) idempotently and returns the persisted
// row. A duplicate (kind, ref_id) is a no-op success (ON CONFLICT DO
// NOTHING) — the caller treats it as already-blocked, never an error, and
// gets back the pre-existing row. created_at is left to the DB default
// (now()/CURRENT_TIMESTAMP). A follow-up SELECT resolves the surrogate id
// on both dialects regardless of whether the write inserted or conflicted.
func (r *BlocklistRepository) Insert(ctx context.Context, kind disco.BlocklistKind, refID int64, label *string) (disco.BlocklistEntry, error) {
	m := discoveryBlocklistModel{
		Kind:  string(kind),
		RefID: refID,
		Label: label,
	}
	// Omit id + created_at so the DB assigns them; DoNothing on the unique
	// (kind, ref_id) index makes the insert idempotent.
	err := r.db.WithContext(ctx).
		Omit("id", "created_at").
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "kind"}, {Name: "ref_id"}},
			DoNothing: true,
		}).
		Create(&m).Error
	if err != nil {
		return disco.BlocklistEntry{}, fmt.Errorf("discovery blocklist insert: %w", err)
	}
	var stored discoveryBlocklistModel
	if err := r.db.WithContext(ctx).
		Select("id", "kind", "ref_id", "label").
		Where("kind = ? AND ref_id = ?", string(kind), refID).
		First(&stored).Error; err != nil {
		return disco.BlocklistEntry{}, fmt.Errorf("discovery blocklist insert readback: %w", err)
	}
	return disco.BlocklistEntry{
		ID:    stored.ID,
		Kind:  disco.BlocklistKind(stored.Kind),
		RefID: stored.RefID,
		Label: stored.Label,
	}, nil
}

// DeleteByID removes one row. Deleting a non-existent id is a no-op
// success (idempotent DELETE) — the handler returns 204 regardless.
func (r *BlocklistRepository) DeleteByID(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&discoveryBlocklistModel{}).Error; err != nil {
		return fmt.Errorf("discovery blocklist delete %d: %w", id, err)
	}
	return nil
}

// LoadBlockSets returns the tmdb + keyword ref_id sets for the in-memory
// cache. Two slices (not maps) — the cache builds the sets. Satisfies
// app.BlocklistLoader structurally.
func (r *BlocklistRepository) LoadBlockSets(ctx context.Context) (tmdbIDs []int64, keywordIDs []int64, err error) {
	var rows []discoveryBlocklistModel
	if err := r.db.WithContext(ctx).
		Select("kind", "ref_id").
		Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("discovery blocklist load sets: %w", err)
	}
	for _, m := range rows {
		switch disco.BlocklistKind(m.Kind) {
		case disco.BlocklistKindTMDB:
			tmdbIDs = append(tmdbIDs, m.RefID)
		case disco.BlocklistKindKeyword:
			keywordIDs = append(keywordIDs, m.RefID)
		}
	}
	return tmdbIDs, keywordIDs, nil
}

// ResolvedBlocklistRow is one GET /discovery/blocklist entry with the
// display title + poster resolved for tmdb rows. Title/PosterAsset are the
// series_texts/series_media_texts values joined via series.tmdb_id (NULL
// when the tmdb_id is unknown to the local catalog). For keyword rows
// Title/PosterAsset are NULL and the handler falls back to Label.
type ResolvedBlocklistRow struct {
	ID          int64   `gorm:"column:id"`
	Kind        string  `gorm:"column:kind"`
	RefID       int64   `gorm:"column:ref_id"`
	Label       *string `gorm:"column:label"`
	Title       *string `gorm:"column:title"`
	PosterAsset *string `gorm:"column:poster_asset"`
}

// ListResolved returns every blocklist row newest-first, LEFT-JOINing the
// canonical series (by tmdb_id) so tmdb rows carry a display title +
// poster. The title/poster subselects mirror ListRepository.GetRanked:
// series_texts with the requested language → en-US fallback, and
// series_media_texts.poster_asset likewise. Keyword rows never match the
// join → Title/PosterAsset stay NULL.
func (r *BlocklistRepository) ListResolved(ctx context.Context, language string) ([]ResolvedBlocklistRow, error) {
	const q = `
		SELECT b.id, b.kind, b.ref_id, b.label,
		       CASE WHEN b.kind = 'tmdb' THEN
		         (SELECT st.title FROM series_texts st
		            WHERE st.series_id = s.id
		            ORDER BY CASE WHEN st.language = ? THEN 2 WHEN st.language = 'en-US' THEN 1 ELSE 0 END DESC,
		                     st.language ASC LIMIT 1)
		       END AS title,
		       CASE WHEN b.kind = 'tmdb' THEN
		         (SELECT smt.poster_asset FROM series_media_texts smt
		            WHERE smt.series_id = s.id
		              AND smt.poster_asset IS NOT NULL AND smt.poster_asset <> ''
		              AND (smt.language = ? OR smt.language = 'en-US')
		            ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC,
		                     smt.language ASC LIMIT 1)
		       END AS poster_asset
		  FROM discovery_blocklist b
		  LEFT JOIN series s ON b.kind = 'tmdb' AND s.tmdb_id = b.ref_id
		 ORDER BY b.id DESC`
	var rows []ResolvedBlocklistRow
	if err := r.db.WithContext(ctx).
		Raw(q, language, language, language).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("discovery blocklist list resolved: %w", err)
	}
	return rows, nil
}
