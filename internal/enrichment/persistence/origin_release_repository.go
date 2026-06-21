package persistence

import (
	"context"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// OriginReleaseRepository wrapped the legacy `origin_releases` table.
// The table was dropped in D-1 — origin tracking is not part of the
// new schema; grab + watchdog reads/writes are owned by the D-6
// rewrite.
//
// D-3 (story 464c) drops the backing schema. The repo stays so the
// existing grab.UseCase / watchdog wiring compiles; every method
// panics with the canonical "pending D-6" sentinel.
type OriginReleaseRepository struct {
	db *gorm.DB
}

func NewOriginReleaseRepository(db *gorm.DB) *OriginReleaseRepository {
	return &OriginReleaseRepository{db: db}
}

func (r *OriginReleaseRepository) Get(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) (ports.OriginRelease, bool, error) {
	_, _, _, _ = ctx, instance, seriesID, season
	panic("not implemented — pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md); origin_releases dropped in D-1")
}

func (r *OriginReleaseRepository) Upsert(ctx context.Context, rec ports.OriginRelease) error {
	_, _ = ctx, rec
	panic("not implemented — pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md); origin_releases dropped in D-1")
}

var _ ports.OriginReleaseRepository = (*OriginReleaseRepository)(nil)
