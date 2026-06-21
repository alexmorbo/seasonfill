package persistence

import (
	"context"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// AppSettingsRepository is the GORM-backed CRUD surface for the
// singleton app_settings row.
type AppSettingsRepository struct {
	db *gorm.DB
}

func NewAppSettingsRepository(db *gorm.DB) *AppSettingsRepository {
	return &AppSettingsRepository{db: db}
}

// GetTimezone is a D-2 boot-survival stub. The legacy app_settings table is
// gone. The tz resolver (internal/runtime/tz/resolver.go:64) tolerates
// the ErrNotFound error path — it logs a WARN and falls back to the TZ
// env var / UTC. Pending D-5 admin+auth rewrite to re-home the
// operator-selected timezone.
func (r *AppSettingsRepository) GetTimezone(ctx context.Context) (string, error) {
	_ = ctx
	return "", ports.ErrNotFound
}

// SetTimezone is a panic stub pending D-5 admin+auth rewrite.
func (r *AppSettingsRepository) SetTimezone(ctx context.Context, tzName string) error {
	_ = ctx
	_ = tzName
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}
