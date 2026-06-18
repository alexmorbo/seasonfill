package ports

import (
	"context"
	"log/slog"
)

// RequestContext carries per-request observability state propagated through
// the request scope via context.Context. The Logger is pre-domained
// (domain="http" for HTTP-scoped requests, see DomainLogger) and pre-bound
// with the request's trace_id. Handlers extract via LoggerFromContext(ctx)
// and emit logs without manually threading attributes.
type RequestContext struct {
	Logger  *slog.Logger
	TraceID string
}

type requestContextKey struct{}

// WithRequestContext attaches rc to ctx. Subsequent FromContext /
// LoggerFromContext calls on derived contexts will return rc. If a
// RequestContext is already present in ctx the new one replaces it
// (last-write-wins; same semantics as context.WithValue).
func WithRequestContext(ctx context.Context, rc RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, rc)
}

// FromContext returns the RequestContext attached to ctx, or the zero value
// (Logger == nil, TraceID == "") if none is present. Callers typically use
// LoggerFromContext instead.
func FromContext(ctx context.Context) RequestContext {
	if ctx == nil {
		return RequestContext{}
	}
	rc, _ := ctx.Value(requestContextKey{}).(RequestContext)
	return rc
}

// LoggerFromContext returns the request-scoped logger from ctx. If ctx has
// no RequestContext (background / non-request scope) it returns
// slog.Default() — the same handler-wrapped JSON logger built at boot in
// cmd/server/server.go, which still auto-injects trace_id via
// internal/logger.contextHandler when the ctx carries one. Never returns
// nil.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	rc := FromContext(ctx)
	if rc.Logger != nil {
		return rc.Logger
	}
	return slog.Default()
}
