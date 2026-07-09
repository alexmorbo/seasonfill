package media

import appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"

// SentinelMissingHash re-exports the mediaproxy missing-art sentinel hash from
// the sanctioned shared/media indirection layer, so OTHER bounded contexts
// (seriesdetail, discovery) can reference it without importing mediaproxy/app
// directly (app→app coupling). shared/media already depends on mediaproxy/app
// for the resolver, and mediaproxy/app owns the canonical definition, so this
// is a single-source re-export, not a second source of truth. Story 1111 F-05.
var SentinelMissingHash = appmedia.SentinelMissingHash

// IsSentinel reports whether hash points at the missing-art sentinel value. A
// nil pointer (no hash resolved) is NOT the sentinel. Lets a caller classify a
// sentinel poster/backdrop without importing mediaproxy/app.
func IsSentinel(hash *string) bool {
	return hash != nil && *hash == appmedia.SentinelMissingHash
}
