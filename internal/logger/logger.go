package logger

import (
	"context"
	"io"
	"log/slog"
	"time"
)

type ctxKey string

const traceIDKey ctxKey = "trace_id"

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

type Config struct {
	Level  string
	Format string
	Output io.Writer

	// DomainLevels holds per-domain slog level overrides (ADR-0006 Axis 2),
	// keyed by the "domain" attribute value (webhook, enrichment, …). Nil or
	// empty means no per-domain overrides.
	DomainLevels map[string]slog.Level
	// DefaultDomainLevel is the threshold applied to records whose domain has
	// no explicit override (and to records with no domain). Nil = fall back to
	// the app Level, so an unset SEASONFILL_LOG_DOMAIN_LEVELS is a strict no-op.
	DefaultDomainLevel *slog.Level
}

type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := TraceID(ctx); id != "" {
		r.AddAttrs(slog.String("trace_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func New(cfg Config) *slog.Logger {
	appLevel := parseLevel(cfg.Level)

	// ADR-0006 Axis 2: resolve the default domain threshold. A nil
	// DefaultDomainLevel means "no explicit default" → use the app level so an
	// unset env changes nothing.
	defaultLevel := appLevel
	if cfg.DefaultDomainLevel != nil {
		defaultLevel = *cfg.DefaultDomainLevel
	}

	// Wrap ONLY when the operator actually configured domain verbosity. No
	// overrides AND a default equal to the app level → strict no-op: the base
	// handler keeps the app level and behaves exactly as before this change.
	wrap := len(cfg.DomainLevels) > 0 || defaultLevel != appLevel

	// When wrapping, the DomainLevelHandler owns all gating; the base handler
	// must not gate below any configured threshold. Drop the base floor to the
	// minimum of every threshold so an elevated per-domain DEBUG still reaches
	// output even when the app level is INFO.
	baseLevel := appLevel
	if wrap {
		baseLevel = minLevel(defaultLevel, cfg.DomainLevels)
	}

	opts := &slog.HandlerOptions{
		Level: baseLevel,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.UTC().Format(time.RFC3339Nano))
				}
			}
			return a
		},
	}

	var base slog.Handler
	if cfg.Format == "text" {
		base = slog.NewTextHandler(cfg.Output, opts)
	} else {
		base = slog.NewJSONHandler(cfg.Output, opts)
	}

	var h slog.Handler = contextHandler{Handler: base}
	if wrap {
		h = &DomainLevelHandler{
			inner:        h,
			levels:       cfg.DomainLevels,
			defaultLevel: defaultLevel,
		}
	}

	return slog.New(h)
}

// minLevel returns the lowest (most verbose) level across the default and all
// per-domain thresholds.
func minLevel(defaultLevel slog.Level, domains map[string]slog.Level) slog.Level {
	m := defaultLevel
	for _, l := range domains {
		if l < m {
			m = l
		}
	}
	return m
}

func parseLevel(l string) slog.Level {
	switch l {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
