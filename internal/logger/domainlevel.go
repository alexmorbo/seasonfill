package logger

import (
	"context"
	"log/slog"
)

// DomainLevelHandler is an slog.Handler middleware that gates records by the
// "domain" attribute (ADR-0006 Axis 2). Each seasonfill subsystem tags its
// logger via sharedports.DomainLogger(base, "webhook"|"enrichment"|…), which
// calls base.With(slog.String("domain", …)) — so the domain reaches a handler
// through the WithAttrs path, NOT as a per-record attribute. This handler
// captures that domain in WithAttrs and drops any record whose level is below
// the domain's configured threshold, letting the operator crank verbosity on
// the one subsystem under test on delta while holding the rest at INFO.
type DomainLevelHandler struct {
	inner        slog.Handler
	levels       map[string]slog.Level
	defaultLevel slog.Level
	// domain is the value captured from a "domain" attr seen in WithAttrs,
	// empty until a DomainLogger wrap flows through. Handle scans the record's
	// own attrs as a fallback for the rare per-call domain attr.
	domain string
}

var _ slog.Handler = (*DomainLevelHandler)(nil)

// thresholdFor resolves the minimum level a record for the given domain must
// meet. Unknown/empty domain → defaultLevel.
func (h *DomainLevelHandler) thresholdFor(domain string) slog.Level {
	if lvl, ok := h.levels[domain]; ok {
		return lvl
	}
	return h.defaultLevel
}

// Enabled gates on the captured domain's threshold. The domain is known here
// only if it arrived via WithAttrs (the production path — DomainLogger), so an
// empty domain resolves to defaultLevel. Handle re-derives the domain from the
// record for the rare per-call domain attr and is the final authority.
func (h *DomainLevelHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.thresholdFor(h.domain)
}

// Handle resolves the effective domain (captured, else scanned off the record)
// and drops the record when its level is below the domain's threshold; a drop
// returns nil WITHOUT calling the inner handler, so nothing is written.
func (h *DomainLevelHandler) Handle(ctx context.Context, rec slog.Record) error {
	domain := h.domain
	if domain == "" {
		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == "domain" {
				domain = a.Value.String()
				return false
			}
			return true
		})
	}
	if rec.Level < h.thresholdFor(domain) {
		return nil
	}
	return h.inner.Handle(ctx, rec)
}

// WithAttrs captures a "domain" attr when present (this is how DomainLogger's
// domain reaches the handler) and always forwards the attrs to the inner
// handler so downstream formatting is unchanged. Non-domain WithAttrs calls
// keep the current captured domain.
func (h *DomainLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	domain := h.domain
	for _, a := range attrs {
		if a.Key == "domain" {
			domain = a.Value.String()
		}
	}
	return &DomainLevelHandler{
		inner:        h.inner.WithAttrs(attrs),
		levels:       h.levels,
		defaultLevel: h.defaultLevel,
		domain:       domain,
	}
}

// WithGroup forwards to the inner handler and preserves the level config and
// captured domain.
func (h *DomainLevelHandler) WithGroup(name string) slog.Handler {
	return &DomainLevelHandler{
		inner:        h.inner.WithGroup(name),
		levels:       h.levels,
		defaultLevel: h.defaultLevel,
		domain:       h.domain,
	}
}
