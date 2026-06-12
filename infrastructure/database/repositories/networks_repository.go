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
)

// NetworksRepository persists the `networks` table + the
// `series_networks` join. Upsert is idempotent on the natural key
// (tmdb_id); Set replaces the full join set for a series in one
// transaction.
type NetworksRepository struct {
	db *gorm.DB
}

func NewNetworksRepository(db *gorm.DB) *NetworksRepository {
	return &NetworksRepository{db: db}
}

// Get fetches by primary key. Returns ports.ErrNotFound on miss.
func (r *NetworksRepository) Get(ctx context.Context, id int64) (taxonomy.Network, error) {
	var m database.NetworkModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.Network{}, ports.ErrNotFound
		}
		return taxonomy.Network{}, fmt.Errorf("get network: %w", err)
	}
	return toNetwork(m), nil
}

// ResolveByName resolves the canonical networks.id by name. networks
// has no UQ on name (only partial UQ on tmdb_id) — the helper picks
// the lowest id when duplicates exist (deterministic). Returns
// ports.ErrNotFound on miss.
func (r *NetworksRepository) ResolveByName(ctx context.Context, name string) (int64, error) {
	if name == "" {
		return 0, fmt.Errorf("resolve network by name: name must be non-empty")
	}
	var m database.NetworkModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("name = ?", name).
		Order("id ASC").
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ports.ErrNotFound
		}
		return 0, fmt.Errorf("resolve network by name: %w", err)
	}
	return m.ID, nil
}

// GetByTMDBID looks up the row by TMDB id. The partial unique index
// guarantees at most one row. Returns ports.ErrNotFound on miss.
func (r *NetworksRepository) GetByTMDBID(ctx context.Context, tmdbID int) (taxonomy.Network, error) {
	var m database.NetworkModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.Network{}, ports.ErrNotFound
		}
		return taxonomy.Network{}, fmt.Errorf("get network by tmdb_id: %w", err)
	}
	return toNetwork(m), nil
}

// ListByIDs returns rows for the given ids in id-ascending order;
// missing ids are silently skipped.
func (r *NetworksRepository) ListByIDs(ctx context.Context, ids []int64) ([]taxonomy.Network, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var models []database.NetworkModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id IN ?", ids).
		Order("id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	out := make([]taxonomy.Network, 0, len(models))
	for _, m := range models {
		out = append(out, toNetwork(m))
	}
	return out, nil
}

// Upsert inserts or updates by natural key (tmdb_id) when present,
// otherwise by PK (id). Idempotent: a no-op upsert mutates only
// updated_at.
func (r *NetworksRepository) Upsert(ctx context.Context, n taxonomy.Network) (int64, error) {
	if n.Name == "" {
		return 0, fmt.Errorf("upsert network: name must be non-empty")
	}
	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	m := fromNetwork(n)

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	var conflict clause.OnConflict
	switch {
	case m.ID != 0:
		conflict = clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns(networkUpdateCols()),
		}
	case m.TMDBID != nil:
		// Partial unique on tmdb_id WHERE tmdb_id IS NOT NULL — both
		// engines require the index predicate be repeated in the
		// ON CONFLICT target so the planner picks the partial index.
		conflict = clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.AssignmentColumns(networkUpdateCols()),
		}
	default:
		// No PK and no natural key — pure insert (Sonarr-fallback
		// path may legitimately land here with tmdb_id NULL).
		conflict = clause.OnConflict{DoNothing: false}
	}
	if err := db.Clauses(conflict).Create(&m).Error; err != nil {
		return 0, fmt.Errorf("upsert network: %w", err)
	}
	return m.ID, nil
}

func networkUpdateCols() []string {
	return []string{"tmdb_id", "name", "logo_asset", "origin_country", "updated_at"}
}

// Set replaces the full series_networks set for seriesID with the
// given network ids, in a single transaction (DELETE + INSERT).
// Position is preserved as the input index (0-based) so callers can
// pass TMDB-ordered ids and get TMDB-ordered rows back. Idempotent:
// re-running with the same ids yields zero row delta in steady state.
//
// Empty ids slice clears the set for seriesID. Caller is responsible
// for the network rows existing (FK is application-side).
func (r *NetworksRepository) Set(ctx context.Context, seriesID int64, networkIDs []int64) error {
	if seriesID == 0 {
		return fmt.Errorf("set series_networks: series_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("series_id = ?", seriesID).
			Delete(&database.SeriesNetworkModel{}).Error; err != nil {
			return fmt.Errorf("set series_networks: clear: %w", err)
		}
		if len(networkIDs) == 0 {
			return nil
		}
		rows := make([]database.SeriesNetworkModel, 0, len(networkIDs))
		for i, nid := range networkIDs {
			pos := i
			rows = append(rows, database.SeriesNetworkModel{
				SeriesID:  seriesID,
				NetworkID: nid,
				Position:  &pos,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("set series_networks: insert: %w", err)
		}
		return nil
	})
}

// ListBySeries returns the network ids for the given series in
// position-ascending order (NULL positions last).
func (r *NetworksRepository) ListBySeries(ctx context.Context, seriesID int64) ([]int64, error) {
	var rows []database.SeriesNetworkModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("position ASC, network_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list series_networks: %w", err)
	}
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.NetworkID)
	}
	return out, nil
}

func toNetwork(m database.NetworkModel) taxonomy.Network {
	return taxonomy.Network{
		ID:            m.ID,
		TMDBID:        m.TMDBID,
		Name:          m.Name,
		LogoAsset:     m.LogoAsset,
		OriginCountry: m.OriginCountry,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromNetwork(n taxonomy.Network) database.NetworkModel {
	return database.NetworkModel{
		ID:            n.ID,
		TMDBID:        n.TMDBID,
		Name:          n.Name,
		LogoAsset:     n.LogoAsset,
		OriginCountry: n.OriginCountry,
		CreatedAt:     n.CreatedAt,
		UpdatedAt:     n.UpdatedAt,
	}
}
