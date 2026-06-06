package webhookinstall

import (
	"net/url"
	"strings"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

const webhookPathPrefix = "/api/v1/webhook/sonarr/"

// CanonicalPath returns the seasonfill-side webhook path for an
// instance. Exported so callers both construct expected URLs and
// match existing ones against the same string.
func CanonicalPath(instanceName string) string {
	return webhookPathPrefix + instanceName
}

// MatchesWebhookURL reports whether the supplied Sonarr notification's
// `url` field points at the seasonfill webhook for `instanceName`.
// Match is path-suffix only: scheme/host/port/query are ignored so
// the reconciler recognises a previously-installed webhook even when
// the operator later changes WebhookURLOverride or the global public
// URL (no duplicate Sonarr entries — reconciler rewrites in place).
//
// Edge cases:
//   - trailing slash: stripped before compare.
//   - query string: ignored (url.Parse splits it off).
//   - malformed URL (parse error or empty Path): fallback to
//     case-insensitive substring on the canonical path. Safe because
//     the canonical path embeds the unique instance name.
//   - case sensitivity: instance names are ASCII per the validator —
//     compare is byte-equal on Path so two instances differing only
//     in case never collide.
func MatchesWebhookURL(fields []sonarr.NotificationField, instanceName string) bool {
	canonical := CanonicalPath(instanceName)
	for _, f := range fields {
		if f.Name != "url" {
			continue
		}
		s, ok := f.Value.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if u, err := url.Parse(s); err == nil && u.Path != "" {
			p := strings.TrimRight(u.Path, "/")
			if p == canonical {
				return true
			}
			continue
		}
		if strings.Contains(strings.ToLower(s), strings.ToLower(canonical)) {
			return true
		}
	}
	return false
}
