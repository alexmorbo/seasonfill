package persistence

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type SonarrInstanceRepository struct{ db *gorm.DB }

func NewSonarrInstanceRepository(db *gorm.DB) *SonarrInstanceRepository {
	return &SonarrInstanceRepository{db: db}
}

// List returns every sonarr_instance row converted to a runtime
// snapshot.
func (r *SonarrInstanceRepository) List(ctx context.Context, c *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	// D-2 boot-survival stub. The legacy sonarr_instance table
	// (id bigserial + 20+ ranking columns) was replaced by the new
	// shape (name TEXT PK, 5 columns) at the schema level. The
	// SonarrInstanceModel struct's columns no longer match the
	// table — any SELECT panics with "missing column ranking_origin_bonus".
	// Returning empty slice + nil lets bootstrap.BuildRuntimeConfig
	// proceed (consumers tolerate an empty instances list). Pending
	// D-5 admin+auth rewrite to the new shape (name TEXT PK).
	_ = ctx
	_ = c
	return nil, nil
}

func (r *SonarrInstanceRepository) GetByName(ctx context.Context, name string, c *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	_ = ctx
	_ = name
	_ = c
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func (r *SonarrInstanceRepository) Create(ctx context.Context, inst runtime.InstanceSnapshot, c *crypto.Cipher) (uint, error) {
	_ = ctx
	_ = inst
	_ = c
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func (r *SonarrInstanceRepository) UpdateWithOptions(
	ctx context.Context,
	inst runtime.InstanceSnapshot,
	c *crypto.Cipher,
	preserveSecret bool,
	ifUnmodifiedSince *time.Time,
) error {
	_ = ctx
	_ = inst
	_ = c
	_ = preserveSecret
	_ = ifUnmodifiedSince
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func (r *SonarrInstanceRepository) Delete(ctx context.Context, name string) error {
	_ = ctx
	_ = name
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func (r *SonarrInstanceRepository) GetUpdatedAt(ctx context.Context, name string) (time.Time, error) {
	_ = ctx
	_ = name
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

var _ ports.SonarrInstanceRepository = (*SonarrInstanceRepository)(nil)
