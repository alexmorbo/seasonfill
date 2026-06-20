package httpx

import (
	"net/http"
	"regexp"
)

// tmdbCDNSizeRule matches image.tmdb.org's `/t/p/{size}/{hash}.ext`
// path. We bucket on `{size}` because the sizes are a closed set, the
// hash is the asset ID (exploding cardinality), and the operator's
// useful signal is "w500 is slow / w1280 is timing out".
var tmdbCDNSizeRule = regexp.MustCompile(`^/t/p/(w92|w154|w185|w300|w342|w500|w780|w1280|original)/.+\.(jpg|png|svg)$`)

// TMDBCDNEndpointFor returns `/t/p/{size}` for a recognised CDN path,
// `/unknown` otherwise. Bounded cardinality even if TMDB introduces
// a new size — the new size falls into "/unknown" until the rule is
// extended.
func TMDBCDNEndpointFor(r *http.Request) string {
	path := r.URL.Path
	matches := tmdbCDNSizeRule.FindStringSubmatch(path)
	if len(matches) < 2 {
		return "/unknown"
	}
	return "/t/p/" + matches[1]
}
