package logger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

const DefaultSlowThreshold = 200 * time.Millisecond

type GormConfig struct {
	SlowThreshold             time.Duration
	IgnoreRecordNotFoundError bool
	LogLevel                  gormlogger.LogLevel
}

func NewGormLogger(log *slog.Logger, cfg GormConfig) gormlogger.Interface {
	if log == nil {
		log = slog.Default()
	}
	if cfg.SlowThreshold == 0 {
		cfg.SlowThreshold = DefaultSlowThreshold
	}
	if cfg.LogLevel == 0 {
		cfg.LogLevel = gormlogger.Warn
	}
	return &gormSlog{log: log, cfg: cfg}
}

type gormSlog struct {
	log *slog.Logger
	cfg GormConfig
}

func (g *gormSlog) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	cp := *g
	cp.cfg.LogLevel = level
	return &cp
}

func (g *gormSlog) Info(ctx context.Context, msg string, data ...interface{}) {
	if g.cfg.LogLevel < gormlogger.Info {
		return
	}
	g.log.LogAttrs(ctx, slog.LevelInfo, "gorm.info",
		slog.String("caller", utils.FileWithLineNum()),
		slog.String("detail", formatMsg(msg, data...)),
	)
}

func (g *gormSlog) Warn(ctx context.Context, msg string, data ...interface{}) {
	if g.cfg.LogLevel < gormlogger.Warn {
		return
	}
	g.log.LogAttrs(ctx, slog.LevelWarn, "gorm.warn",
		slog.String("caller", utils.FileWithLineNum()),
		slog.String("detail", formatMsg(msg, data...)),
	)
}

func (g *gormSlog) Error(ctx context.Context, msg string, data ...interface{}) {
	if g.cfg.LogLevel < gormlogger.Error {
		return
	}
	g.log.LogAttrs(ctx, slog.LevelError, "gorm.error",
		slog.String("caller", utils.FileWithLineNum()),
		slog.String("detail", formatMsg(msg, data...)),
	)
}

func (g *gormSlog) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if g.cfg.LogLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	elapsedMs := float64(elapsed.Nanoseconds()) / 1e6
	caller := utils.FileWithLineNum()

	switch {
	case err != nil && g.cfg.LogLevel >= gormlogger.Error &&
		(!errors.Is(err, gorm.ErrRecordNotFound) || !g.cfg.IgnoreRecordNotFoundError):
		sql, rows := fc()
		g.log.LogAttrs(ctx, slog.LevelError, "gorm.query.error",
			slog.String("caller", caller),
			slog.String("error", err.Error()),
			slog.Float64("duration_ms", elapsedMs),
			slog.Int64("rows", rows),
			slog.String("sql", sql),
		)
	case g.cfg.SlowThreshold > 0 && elapsed > g.cfg.SlowThreshold && g.cfg.LogLevel >= gormlogger.Warn:
		sql, rows := fc()
		g.log.LogAttrs(ctx, slog.LevelWarn, "gorm.query.slow",
			slog.String("caller", caller),
			slog.Duration("slow_threshold", g.cfg.SlowThreshold),
			slog.Float64("duration_ms", elapsedMs),
			slog.Int64("rows", rows),
			slog.String("sql", sql),
		)
	default:
		if !g.log.Enabled(ctx, slog.LevelDebug) {
			return
		}
		sql, rows := fc()
		attrs := []slog.Attr{
			slog.String("caller", caller),
			slog.Float64("duration_ms", elapsedMs),
			slog.Int64("rows", rows),
			slog.String("sql", sql),
		}
		if err != nil {
			attrs = append(attrs, slog.String("error", err.Error()))
		}
		g.log.LogAttrs(ctx, slog.LevelDebug, "gorm.query", attrs...)
	}
}

func formatMsg(msg string, data ...interface{}) string {
	if len(data) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, data...)
}
