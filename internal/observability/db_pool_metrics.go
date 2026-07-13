package observability

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/VictoriaMetrics/metrics"
	"gorm.io/gorm"
)

// M-3 — DB connection-pool observability + per-repo write-error counter.
//
// Part A publishes database/sql pool stats as VictoriaMetrics callback-gauges
// (read at scrape time; no background goroutine). Part B registers GORM
// after-write callbacks that count statement-level write errors per table.
//
// Both are wired exactly once from internal/wiring/bootstrap.go::BuildPersistence.

// dbPoolOnce guards Part A so RegisterDBPoolMetrics is safe to call more than
// once (the callback closures bind to the FIRST *sql.DB). GetOrCreateGauge is
// itself idempotent, so this Once is belt-and-suspenders: it keeps the bound
// handle stable and avoids re-allocating 8 closures on repeated calls.
var dbPoolOnce sync.Once

// RegisterDBPoolMetrics registers 8 callback-gauges that read db.Stats() at
// scrape time. Idempotent (sync.Once); nil-safe (no-op on a nil handle so the
// wiring guard can call it unconditionally).
func RegisterDBPoolMetrics(db *sql.DB) {
	if db == nil {
		return
	}
	dbPoolOnce.Do(func() {
		metrics.GetOrCreateGauge(`seasonfill_db_pool_max_open`, func() float64 {
			return float64(db.Stats().MaxOpenConnections)
		})
		metrics.GetOrCreateGauge(`seasonfill_db_pool_open_connections`, func() float64 {
			return float64(db.Stats().OpenConnections)
		})
		metrics.GetOrCreateGauge(`seasonfill_db_pool_in_use`, func() float64 {
			return float64(db.Stats().InUse)
		})
		metrics.GetOrCreateGauge(`seasonfill_db_pool_idle`, func() float64 {
			return float64(db.Stats().Idle)
		})
		metrics.GetOrCreateGauge(`seasonfill_db_pool_wait_count`, func() float64 {
			return float64(db.Stats().WaitCount)
		})
		metrics.GetOrCreateGauge(`seasonfill_db_pool_wait_duration_seconds`, func() float64 {
			return db.Stats().WaitDuration.Seconds()
		})
		metrics.GetOrCreateGauge(`seasonfill_db_pool_max_idle_closed`, func() float64 {
			return float64(db.Stats().MaxIdleClosed)
		})
		metrics.GetOrCreateGauge(`seasonfill_db_pool_max_lifetime_closed`, func() float64 {
			return float64(db.Stats().MaxLifetimeClosed)
		})
	})
}

// GORM after-write callback names. Namespaced so they never collide with
// GORM's own "gorm:create"/"gorm:update"/"gorm:delete" processors.
const (
	cbWriteErrorCreate = "seasonfill:write_error_create"
	cbWriteErrorUpdate = "seasonfill:write_error_update"
	cbWriteErrorDelete = "seasonfill:write_error_delete"
)

// RegisterDBWriteErrorMetrics registers After-Create/Update/Delete callbacks on
// gormDB that increment seasonfill_db_write_errors_total{repo,op} whenever a
// write statement finishes with a non-nil error. Called once on the primary
// *gorm.DB from wiring; each callback is registered per-DB, so there is no
// process-global duplicate-registration hazard.
func RegisterDBWriteErrorMetrics(gormDB *gorm.DB) error {
	if gormDB == nil {
		return errors.New("observability: RegisterDBWriteErrorMetrics: nil gormDB")
	}
	if err := gormDB.Callback().Create().After("gorm:create").
		Register(cbWriteErrorCreate, writeErrorCallback("create")); err != nil {
		return fmt.Errorf("register create write-error callback: %w", err)
	}
	if err := gormDB.Callback().Update().After("gorm:update").
		Register(cbWriteErrorUpdate, writeErrorCallback("update")); err != nil {
		return fmt.Errorf("register update write-error callback: %w", err)
	}
	if err := gormDB.Callback().Delete().After("gorm:delete").
		Register(cbWriteErrorDelete, writeErrorCallback("delete")); err != nil {
		return fmt.Errorf("register delete write-error callback: %w", err)
	}
	return nil
}

// writeErrorCallback returns a GORM callback that ticks the per-repo write-error
// counter when the statement carries a real write error. op ∈ {create,update,
// delete}; repo is the GORM table name. ErrRecordNotFound is not a write
// failure and is skipped. Labels are baked into the metric-name string per the
// VictoriaMetrics idiom used across observability/metrics.go.
func writeErrorCallback(op string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Error == nil {
			return
		}
		if errors.Is(db.Statement.Error, gorm.ErrRecordNotFound) {
			return
		}
		table := db.Statement.Table
		if table == "" {
			table = "unknown"
		}
		metrics.GetOrCreateCounter(
			fmt.Sprintf(`seasonfill_db_write_errors_total{repo=%q,op=%q}`, table, op),
		).Inc()
	}
}
