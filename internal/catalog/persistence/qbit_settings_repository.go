package persistence

import (
	"context"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type QbitSettingsRepository struct {
	db *gorm.DB
}

func NewQbitSettingsRepository(db *gorm.DB) *QbitSettingsRepository {
	return &QbitSettingsRepository{db: db}
}

func (r *QbitSettingsRepository) Upsert(ctx context.Context, rec ports.QbitSettingsRecord) error {
	_ = ctx
	_ = rec
	panic("not implemented — pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md)")
}

func (r *QbitSettingsRepository) GetByInstance(ctx context.Context, instanceID uint) (ports.QbitSettingsRecord, error) {
	_ = ctx
	_ = instanceID
	panic("not implemented — pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md)")
}

func (r *QbitSettingsRepository) DeleteByInstance(ctx context.Context, instanceID uint) error {
	_ = ctx
	_ = instanceID
	panic("not implemented — pending D-6 grab+watchdog rewrite (D2-revised-roadmap.md)")
}

// List returns every settings row. D-2 boot-survival stub: called
// synchronously by the OnApplied fanout closure when bus.Publish
// runs at boot — a panic here is NOT recovered (not lifecycle.Go
// wrapped) and would kill the process. Returning empty slice + nil
// lets the fanout proceed; the watchdog regrab + torrentsync loops
// see no qBit settings (no instances configured anyway given
// InstanceRepository.List also returns empty during D-2..D-5).
// Pending D-6 grab+watchdog rewrite.
func (r *QbitSettingsRepository) List(ctx context.Context) ([]ports.QbitSettingsRecord, error) {
	_ = ctx
	return nil, nil
}

var _ ports.QbitSettingsRepository = (*QbitSettingsRepository)(nil)
