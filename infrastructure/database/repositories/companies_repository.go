package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CompaniesRepository persists the `production_companies` table +
// the `series_companies` join. Structurally identical to
// NetworksRepository — same dictionary + join shape.
type CompaniesRepository struct {
	db *gorm.DB
}

func NewCompaniesRepository(db *gorm.DB) *CompaniesRepository {
	return &CompaniesRepository{db: db}
}

func (r *CompaniesRepository) Get(ctx context.Context, id int64) (taxonomy.ProductionCompany, error) {
	var m database.ProductionCompanyModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.ProductionCompany{}, ports.ErrNotFound
		}
		return taxonomy.ProductionCompany{}, fmt.Errorf("get company: %w", err)
	}
	return toCompany(m), nil
}

func (r *CompaniesRepository) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (taxonomy.ProductionCompany, error) {
	var m database.ProductionCompanyModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.ProductionCompany{}, ports.ErrNotFound
		}
		return taxonomy.ProductionCompany{}, fmt.Errorf("get company by tmdb_id: %w", err)
	}
	return toCompany(m), nil
}

func (r *CompaniesRepository) ListByIDs(ctx context.Context, ids []int64) ([]taxonomy.ProductionCompany, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var models []database.ProductionCompanyModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id IN ?", ids).
		Order("id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	out := make([]taxonomy.ProductionCompany, 0, len(models))
	for _, m := range models {
		out = append(out, toCompany(m))
	}
	return out, nil
}

func (r *CompaniesRepository) Upsert(ctx context.Context, c taxonomy.ProductionCompany) (int64, error) {
	if c.Name == "" {
		return 0, fmt.Errorf("upsert company: name must be non-empty")
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	m := fromCompany(c)

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	// No PK + no natural key ⇒ pure INSERT, no ON CONFLICT clause.
	// Previously this branch emitted `clause.OnConflict{DoNothing:
	// false}` which serialized to a bare `ON CONFLICT DO UPDATE`;
	// SQLite tolerates the empty target, Postgres rejects it with
	// SQLSTATE 42601 ("requires inference specification or constraint
	// name"). Story 424a dual-backend migration caught this on the
	// Sonarr-fallback path (tmdb_id NULL).
	switch {
	case m.ID != 0:
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns(companyUpdateCols()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert company: %w", err)
		}
	case m.TMDBID != nil:
		conflict := clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.AssignmentColumns(companyUpdateCols()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert company: %w", err)
		}
	default:
		// No PK and no natural key — pure insert. GORM assigns id.
		if err := db.Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert company: %w", err)
		}
	}
	return m.ID, nil
}

func companyUpdateCols() []string {
	return []string{"tmdb_id", "name", "logo_asset", "origin_country", "updated_at"}
}

// Set replaces the full series_companies set for seriesID. Same
// semantics as NetworksRepository.Set.
func (r *CompaniesRepository) Set(ctx context.Context, seriesID domain.SeriesID, companyIDs []int64) error {
	if seriesID == 0 {
		return fmt.Errorf("set series_companies: series_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("series_id = ?", seriesID).
			Delete(&database.SeriesCompanyModel{}).Error; err != nil {
			return fmt.Errorf("set series_companies: clear: %w", err)
		}
		if len(companyIDs) == 0 {
			return nil
		}
		rows := make([]database.SeriesCompanyModel, 0, len(companyIDs))
		for i, cid := range companyIDs {
			pos := i
			rows = append(rows, database.SeriesCompanyModel{
				SeriesID:  seriesID,
				CompanyID: cid,
				Position:  &pos,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("set series_companies: insert: %w", err)
		}
		return nil
	})
}

func (r *CompaniesRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error) {
	var rows []database.SeriesCompanyModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("position ASC, company_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list series_companies: %w", err)
	}
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.CompanyID)
	}
	return out, nil
}

func toCompany(m database.ProductionCompanyModel) taxonomy.ProductionCompany {
	return taxonomy.ProductionCompany{
		ID:            m.ID,
		TMDBID:        m.TMDBID,
		Name:          m.Name,
		LogoAsset:     m.LogoAsset,
		OriginCountry: m.OriginCountry,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromCompany(c taxonomy.ProductionCompany) database.ProductionCompanyModel {
	return database.ProductionCompanyModel{
		ID:            c.ID,
		TMDBID:        c.TMDBID,
		Name:          c.Name,
		LogoAsset:     c.LogoAsset,
		OriginCountry: c.OriginCountry,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}
