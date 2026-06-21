package persistence

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type RuntimeConfigRepository struct {
	db     *gorm.DB
	cipher *crypto.Cipher
}

func NewRuntimeConfigRepository(db *gorm.DB, cipher *crypto.Cipher) *RuntimeConfigRepository {
	return &RuntimeConfigRepository{db: db, cipher: cipher}
}

func (r *RuntimeConfigRepository) Get(ctx context.Context) (ports.RuntimeConfigRow, error) {
	// D-2 boot-survival stub. The legacy runtime_config table is gone.
	// Bootstrap (apikey.go → ResolveAPIKey; bootstrap.go → BuildRuntimeConfig)
	// reads here at boot to seed the in-memory snapshot. Returning defaults
	// + nil err lets the pod start; subsequent attempted Upsert is a no-op
	// (see below). Pending D-5 admin+auth rewrite to read from the new
	// auth schema (users + app_secret tables).
	_ = ctx
	d := runtime.Defaults()
	return ports.RuntimeConfigRow{
		Cron:            d.Cron,
		Scan:            d.Scan,
		DryRun:          d.DryRun,
		GlobalRateLimit: d.GlobalRateLimit,
		Auth:            d.Auth,
	}, nil
}

// Upsert writes the singleton row. When ifUnmodifiedSince != nil and
// the existing row exists with updated_at strictly newer than the
// header value (second-truncated), returns ports.ErrStaleWrite
// without writing. The "row missing → create fresh" path is taken
// regardless of ifUnmodifiedSince (the first ever PUT can't be stale).
func (r *RuntimeConfigRepository) Upsert(
	ctx context.Context,
	snap runtime.Snapshot,
	ifUnmodifiedSince *time.Time,
) error {
	// D-2 boot-survival stub: no-op. Bootstrap calls this after Get
	// returns sentinel defaults to seed the row — there's no
	// destination on the new schema yet. Pending D-5 admin+auth
	// rewrite.
	_ = ctx
	_ = snap
	_ = ifUnmodifiedSince
	return nil
}

// SaveAPIKey is a no-op during D-2..D-5. Bootstrap's
// internal/wiring/apikey.go calls this after auto-generating
// (or persisting a user-provided) master key. There's no
// destination on the new schema yet — the auto-gen log line
// to stdout still prints, operator can capture and set as
// SEASONFILL_API_KEY env for the next restart. Pending D-5
// admin+auth rewrite.
func (r *RuntimeConfigRepository) SaveAPIKey(ctx context.Context, ct []byte, autoGen bool) error {
	_ = ctx
	_ = ct
	_ = autoGen
	return nil
}

func (r *RuntimeConfigRepository) UpsertOIDCSecret(ctx context.Context, plaintext string) error {
	_ = ctx
	_ = plaintext
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

func (r *RuntimeConfigRepository) DecryptOIDCSecret(ctx context.Context) (string, error) {
	_ = ctx
	panic("not implemented — pending D-5 admin+auth rewrite (D2-revised-roadmap.md)")
}

var _ ports.RuntimeConfigRepository = (*RuntimeConfigRepository)(nil)
