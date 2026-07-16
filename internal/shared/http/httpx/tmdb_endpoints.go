package httpx

import (
	"net/http"
	"regexp"
	"strings"
)

// tmdbEndpointRules collapses TMDB v3 paths with embedded IDs to a
// template form (`/tv/{id}`, `/person/{id}`, …). Order matters: more
// specific patterns BEFORE more general ones (e.g. /tv/{id}/season/{n}
// before /tv/{id}). When a new TMDB path lands in infrastructure/tmdb,
// extend this slice — until then the fall-through case in
// TMDBEndpointFor returns "/unknown".
var tmdbEndpointRules = []struct {
	pattern *regexp.Regexp
	label   string
}{
	{regexp.MustCompile(`^/tv/\d+/season/\d+/credits$`), "/tv/{id}/season/{n}/credits"},
	{regexp.MustCompile(`^/tv/\d+/season/\d+$`), "/tv/{id}/season/{n}"},
	{regexp.MustCompile(`^/tv/\d+/credits$`), "/tv/{id}/credits"},
	{regexp.MustCompile(`^/tv/\d+/recommendations$`), "/tv/{id}/recommendations"},
	{regexp.MustCompile(`^/tv/\d+/external_ids$`), "/tv/{id}/external_ids"},
	{regexp.MustCompile(`^/tv/changes$`), "/tv/changes"},
	{regexp.MustCompile(`^/tv/\d+$`), "/tv/{id}"},
	{regexp.MustCompile(`^/person/\d+/tv_credits$`), "/person/{id}/tv_credits"},
	{regexp.MustCompile(`^/person/\d+$`), "/person/{id}"},
	{regexp.MustCompile(`^/find/\d+$`), "/find/{id}"},
	{regexp.MustCompile(`^/find/[a-z0-9]+$`), "/find/{external_id}"},
	{regexp.MustCompile(`^/search/tv$`), "/search/tv"},
	{regexp.MustCompile(`^/genre/tv/list$`), "/genre/tv/list"},
	{regexp.MustCompile(`^/search/movie$`), "/search/movie"},
	{regexp.MustCompile(`^/search/person$`), "/search/person"},
	{regexp.MustCompile(`^/configuration$`), "/configuration"},
}

// TMDBEndpointFor strips the TMDB v3 "/3" prefix (the production base
// URL is https://api.themoviedb.org/3) and applies tmdbEndpointRules.
// Returns "/unknown" when no rule matches — a predictable bucket for
// new endpoints until the rule set is extended.
func TMDBEndpointFor(r *http.Request) string {
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/3")
	if path == "" {
		path = "/"
	}
	for _, rule := range tmdbEndpointRules {
		if rule.pattern.MatchString(path) {
			return rule.label
		}
	}
	return "/unknown"
}
