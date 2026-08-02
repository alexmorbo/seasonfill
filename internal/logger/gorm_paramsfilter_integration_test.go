package logger

import (
	"bytes"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// TestGormSlog_ParamsFilter_EndToEnd proves the wiring: GORM's Trace callback
// type-asserts db.Logger.(gorm.ParamsFilter) and applies it inside the shared
// fc() closure. At the default warn level a bound secret must NEVER appear in
// the emitted SQL (parameterized); at info it appears (deliberate debug).
func TestGormSlog_ParamsFilter_EndToEnd(t *testing.T) {
	const secret = "supersecret-webhook-token"

	t.Run("warn_parameterized_no_secret", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// SlowThreshold 1ns → every query trips the slow branch, which emits
		// at Warn regardless of query speed; proves parameterization applies
		// to the slow branch too.
		db := openWithLogger(t, buf, GormConfig{
			SlowThreshold: time.Nanosecond,
			LogLevel:      gormlogger.Warn,
		})
		require.NoError(t, db.Exec("SELECT ? AS v", secret).Error)

		out := buf.String()
		require.NotEmpty(t, out, "slow branch must emit at Warn")
		assert.NotContains(t, out, secret, "secret must not leak — SQL must be parameterized")
		assert.Contains(t, out, "?", "parameterized SQL keeps its placeholder")
	})

	t.Run("info_inlined_has_secret", func(t *testing.T) {
		buf := &bytes.Buffer{}
		db := openWithLogger(t, buf, GormConfig{
			SlowThreshold: time.Second,
			LogLevel:      gormlogger.Info,
		})
		require.NoError(t, db.Exec("SELECT ? AS v", secret).Error)

		out := buf.String()
		require.NotEmpty(t, out, "normal branch must emit at Info")
		assert.Contains(t, out, secret, "at Info the SQL is inlined with real values")
	})
}

func openWithLogger(t *testing.T, buf *bytes.Buffer, cfg GormConfig) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: NewGormLogger(captureLogger(buf), cfg),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
