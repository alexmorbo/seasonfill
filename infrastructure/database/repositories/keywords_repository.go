package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// KeywordsRepository persists the `keywords` table + the
// `series_keywords` join. Same shape as GenresRepository. v1
// keywords are en-only (TMDB does not localise the /tv/{id}/keywords
// payload); the unified i18n form ships for forward-compat.
type KeywordsRepository struct {
	db *gorm.DB
}

func NewKeywordsRepository(db *gorm.DB) *KeywordsRepository {
	return &KeywordsRepository{db: db}
}

// Get fetches by primary key and resolves the localised name via the
// shared §5.6 fallback helper. In v1 always returns the en-US row
// because that is the only language seeded.
func (r *KeywordsRepository) Get(ctx context.Context, id int64, language string) (taxonomy.Keyword, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	var m database.KeywordModel
	err := db.Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.Keyword{}, ports.ErrNotFound
		}
		return taxonomy.Keyword{}, fmt.Errorf("get keyword: %w", err)
	}
	k := toKeyword(m)

	var i18n database.KeywordI18nModel
	if err := pickLanguageFallback(
		ctx, r.db,
		"keywords_i18n", "keyword_id",
		id, language,
		&i18n,
	); err != nil {
		return taxonomy.Keyword{}, fmt.Errorf("resolve keyword name: %w", err)
	}
	if i18n.KeywordID != 0 {
		k.Name = i18n.Name
		k.Language = i18n.Language
	}
	return k, nil
}

func (r *KeywordsRepository) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (taxonomy.Keyword, error) {
	var m database.KeywordModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.Keyword{}, ports.ErrNotFound
		}
		return taxonomy.Keyword{}, fmt.Errorf("get keyword by tmdb_id: %w", err)
	}
	return toKeyword(m), nil
}

// ResolveByName maps a keyword string to the canonical keywords.id
// via the keywords_i18n_name index. Forward-compat for future RU /
// de keyword sources; v1 only has en-US rows.
func (r *KeywordsRepository) ResolveByName(ctx context.Context, language, name string) (int64, error) {
	if language == "" {
		return 0, fmt.Errorf("resolve keyword by name: language must be non-empty")
	}
	if name == "" {
		return 0, fmt.Errorf("resolve keyword by name: name must be non-empty")
	}
	var m database.KeywordI18nModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("language = ? AND name = ?", language, name).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ports.ErrNotFound
		}
		return 0, fmt.Errorf("resolve keyword by name: %w", err)
	}
	return m.KeywordID, nil
}

func (r *KeywordsRepository) Upsert(ctx context.Context, k taxonomy.Keyword) (int64, error) {
	now := time.Now().UTC()
	if k.CreatedAt.IsZero() {
		k.CreatedAt = now
	}
	k.UpdatedAt = now
	m := database.KeywordModel{
		ID:        k.ID,
		TMDBID:    k.TMDBID,
		CreatedAt: k.CreatedAt,
		UpdatedAt: k.UpdatedAt,
	}

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	// No PK + no natural key ⇒ pure INSERT, no ON CONFLICT clause.
	// Previously this branch emitted `clause.OnConflict{DoNothing:
	// false}` which serialized to a bare `ON CONFLICT DO UPDATE`;
	// SQLite tolerates the empty target, Postgres rejects it with
	// SQLSTATE 42601 ("requires inference specification or constraint
	// name"). Story 424a dual-backend migration caught this.
	switch {
	case m.ID != 0:
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"tmdb_id", "updated_at"}),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert keyword: %w", err)
		}
	case m.TMDBID != nil:
		conflict := clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.AssignmentColumns([]string{"tmdb_id", "updated_at"}),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert keyword: %w", err)
		}
	default:
		// No PK and no natural key — pure insert. GORM assigns id.
		if err := db.Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert keyword: %w", err)
		}
	}
	return m.ID, nil
}

// Set replaces the full series_keywords set. Keywords have no
// position column per PRD §5.3 (unordered).
func (r *KeywordsRepository) Set(ctx context.Context, seriesID domain.SeriesID, keywordIDs []int64) error {
	if seriesID == 0 {
		return fmt.Errorf("set series_keywords: series_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("series_id = ?", seriesID).
			Delete(&database.SeriesKeywordModel{}).Error; err != nil {
			return fmt.Errorf("set series_keywords: clear: %w", err)
		}
		if len(keywordIDs) == 0 {
			return nil
		}
		rows := make([]database.SeriesKeywordModel, 0, len(keywordIDs))
		for _, kid := range keywordIDs {
			rows = append(rows, database.SeriesKeywordModel{
				SeriesID:  seriesID,
				KeywordID: kid,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("set series_keywords: insert: %w", err)
		}
		return nil
	})
}

func (r *KeywordsRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error) {
	var rows []database.SeriesKeywordModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("keyword_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list series_keywords: %w", err)
	}
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.KeywordID)
	}
	return out, nil
}

func toKeyword(m database.KeywordModel) taxonomy.Keyword {
	return taxonomy.Keyword{
		ID:        m.ID,
		TMDBID:    m.TMDBID,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// KeywordsI18nRepository persists the localised name rows for a
// keyword. Mirrors GenresI18nRepository.
type KeywordsI18nRepository struct {
	db *gorm.DB
}

func NewKeywordsI18nRepository(db *gorm.DB) *KeywordsI18nRepository {
	return &KeywordsI18nRepository{db: db}
}

func (r *KeywordsI18nRepository) Get(ctx context.Context, keywordID int64, language string) (taxonomy.KeywordI18n, error) {
	var m database.KeywordI18nModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("keyword_id = ? AND language = ?", keywordID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.KeywordI18n{}, ports.ErrNotFound
		}
		return taxonomy.KeywordI18n{}, fmt.Errorf("get keywords_i18n: %w", err)
	}
	return taxonomy.KeywordI18n{
		KeywordID: m.KeywordID,
		Language:  m.Language,
		Name:      m.Name,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

func (r *KeywordsI18nRepository) GetWithFallback(ctx context.Context, keywordID int64, language string) (taxonomy.KeywordI18n, error) {
	var m database.KeywordI18nModel
	if err := pickLanguageFallback(ctx, r.db, "keywords_i18n", "keyword_id", keywordID, language, &m); err != nil {
		return taxonomy.KeywordI18n{}, err
	}
	if m.KeywordID == 0 {
		return taxonomy.KeywordI18n{}, ports.ErrNotFound
	}
	return taxonomy.KeywordI18n{
		KeywordID: m.KeywordID,
		Language:  m.Language,
		Name:      m.Name,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

func (r *KeywordsI18nRepository) Upsert(ctx context.Context, t taxonomy.KeywordI18n) error {
	if t.KeywordID == 0 {
		return fmt.Errorf("upsert keywords_i18n: keyword_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert keywords_i18n: language must be non-empty")
	}
	if t.Name == "" {
		return fmt.Errorf("upsert keywords_i18n: name must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := database.KeywordI18nModel{
		KeywordID: t.KeywordID,
		Language:  t.Language,
		Name:      t.Name,
		UpdatedAt: t.UpdatedAt,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "keyword_id"},
			{Name: "language"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"name", "updated_at"}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert keywords_i18n: %w", err)
	}
	return nil
}
