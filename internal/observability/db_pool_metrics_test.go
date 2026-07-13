package observability

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/config"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// m3ProbeRow is a throwaway model used only to trip the write-error callback
// via a duplicate-primary-key create. Fixed table name so the {repo=...} label
// is deterministic.
type m3ProbeRow struct {
	ID   int `gorm:"primaryKey"`
	Name string
}

func (m3ProbeRow) TableName() string { return "m3_probe_rows" }

// openM3TestDB opens a file-backed sqlite DB (schema survives across pooled
// connections, unlike :memory:) and AutoMigrates the probe model.
func openM3TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m3.db")
	db, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: path},
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&m3ProbeRow{}))
	return db
}

// TestRegisterDBMetrics_PoolGaugesAndWriteError is the primary M-3 smoke:
// registers both metric families, runs a successful read + create, then a
// deliberately failing create (duplicate PK) to trip the write-error counter
// with op="create", and asserts the exposition text.
func TestRegisterDBMetrics_PoolGaugesAndWriteError(t *testing.T) {
	db := openM3TestDB(t)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	RegisterDBPoolMetrics(sqlDB)
	require.NoError(t, RegisterDBWriteErrorMetrics(db))

	// Successful read — must NOT trip the write-error counter.
	var n int64
	require.NoError(t, db.Model(&m3ProbeRow{}).Count(&n).Error)

	// Successful create.
	require.NoError(t, db.Create(&m3ProbeRow{ID: 1, Name: "ok"}).Error)

	// Deliberately failing create — duplicate primary key → UNIQUE violation.
	err = db.Create(&m3ProbeRow{ID: 1, Name: "dup"}).Error
	require.Error(t, err, "duplicate-PK insert must fail")

	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	body := buf.String()

	// Part A — all 8 pool gauge NAMES present with a non-negative value.
	poolNames := []string{
		"seasonfill_db_pool_max_open",
		"seasonfill_db_pool_open_connections",
		"seasonfill_db_pool_in_use",
		"seasonfill_db_pool_idle",
		"seasonfill_db_pool_wait_count",
		"seasonfill_db_pool_wait_duration_seconds",
		"seasonfill_db_pool_max_idle_closed",
		"seasonfill_db_pool_max_lifetime_closed",
	}
	for _, name := range poolNames {
		line := findMetricLine(body, name)
		require.NotEmptyf(t, line, "pool gauge %q missing from /metrics:\n%s", name, body)
		val := strings.TrimSpace(strings.TrimPrefix(line, name))
		require.NotEmptyf(t, val, "pool gauge %q has no value: %q", name, line)
		require.Falsef(t, strings.HasPrefix(val, "-"),
			"pool gauge %q negative: %q", name, line)
	}

	// Part B — write-error counter present and incremented for the failed create.
	require.Containsf(t, body,
		`seasonfill_db_write_errors_total{repo="m3_probe_rows",op="create"}`,
		"write-error counter missing/not incremented:\n%s", body)
}

// TestRegisterDBPoolMetrics_Idempotent — repeated calls must not panic
// (sync.Once + GetOrCreateGauge idempotency) and the gauges stay published.
func TestRegisterDBPoolMetrics_Idempotent(t *testing.T) {
	db := openM3TestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)

	RegisterDBPoolMetrics(sqlDB)
	RegisterDBPoolMetrics(sqlDB)

	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	require.Contains(t, buf.String(), "seasonfill_db_pool_open_connections")
}

// TestRegisterDBPoolMetrics_NilSafe — nil handle is a no-op (does not consume
// the Once, does not panic). Order-independent with the other tests.
func TestRegisterDBPoolMetrics_NilSafe(t *testing.T) {
	RegisterDBPoolMetrics(nil)
}

// TestRegisterDBWriteErrorMetrics_NilErrors — nil gormDB returns an error
// rather than panicking, so the wiring can surface it.
func TestRegisterDBWriteErrorMetrics_NilErrors(t *testing.T) {
	require.Error(t, RegisterDBWriteErrorMetrics(nil))
}

// TestWriteErrorCallback_UpdateAndDelete exercises the update+delete branches
// on a table whose row does not exist / violates nothing — asserting the two
// label variants register when a write actually errors. Here we force an error
// by writing against a dropped table.
func TestWriteErrorCallback_UpdateAndDelete(t *testing.T) {
	db := openM3TestDB(t)
	require.NoError(t, RegisterDBWriteErrorMetrics(db))

	// Drop the table so subsequent writes error at the driver.
	require.NoError(t, db.Migrator().DropTable(&m3ProbeRow{}))

	_ = db.Model(&m3ProbeRow{}).Where("id = ?", 1).Update("name", "x").Error
	_ = db.Where("id = ?", 1).Delete(&m3ProbeRow{}).Error

	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	body := buf.String()

	require.Containsf(t, body,
		`seasonfill_db_write_errors_total{repo="m3_probe_rows",op="update"}`,
		"update write-error counter missing:\n%s", body)
	require.Containsf(t, body,
		`seasonfill_db_write_errors_total{repo="m3_probe_rows",op="delete"}`,
		"delete write-error counter missing:\n%s", body)
}

// TestDBPoolGauges_NoTotalSuffix guards the F-11 rename: callback-read gauges
// must not carry the counter-reserved _total suffix. The only db_* metric that
// legitimately ends in _total is the real write-error counter, which lives
// under seasonfill_db_write_errors_total (no "pool" segment).
func TestDBPoolGauges_NoTotalSuffix(t *testing.T) {
	db := openM3TestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	RegisterDBPoolMetrics(sqlDB)

	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if !strings.HasPrefix(line, "seasonfill_db_pool_") {
			continue
		}
		name := strings.Fields(line)[0]
		// Trim any {label...} suffix so we test the bare metric name.
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		require.Falsef(t, strings.HasSuffix(name, "_total"),
			"db_pool gauge %q must not carry the counter-reserved _total suffix", name)
	}
}
