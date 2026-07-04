package ports

import (
	"fmt"
	"log/slog"
)

// AllowedDomains is the closed list of log "domain" attribute values per
// PRD §6.5. Any new domain requires a PRD update — adding to this map
// without PRD governance is an anti-pattern caught at PR review.
var AllowedDomains = map[string]struct{}{
	"http":       {},
	"webhook":    {},
	"scan":       {},
	"tmdb":       {},
	"omdb":       {},
	"sonarr":     {},
	"radarr":     {},
	"qbit":       {},
	"queue":      {},
	"composer":   {},
	"enrichment": {},
	"watchdog":   {},
	"discovery":  {},
	"auth":       {},
	"admin":      {},
	"boot":       {},
	"gc":         {},
	"shutdown":   {},

	"catalog_counts":          {},
	"library_poster_coverage": {},
}

// DomainLogger returns base.With(slog.String("domain", domain)).
//
// Panics if domain is not in AllowedDomains. The panic is intentional and
// fires at wiring time (typically inside a constructor that runs at boot,
// not per-request) so any misuse blows up immediately and never reaches a
// production log line. base must be non-nil; passing nil panics with a
// distinct message.
func DomainLogger(base *slog.Logger, domain string) *slog.Logger {
	if base == nil {
		panic("ports.DomainLogger: base logger must not be nil")
	}
	if _, ok := AllowedDomains[domain]; !ok {
		panic(fmt.Sprintf("ports.DomainLogger: unknown domain %q (see PRD §6.5 closed list)", domain))
	}
	return base.With(slog.String("domain", domain))
}
