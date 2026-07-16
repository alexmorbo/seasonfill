package httpx

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTMDBEndpointFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{"/3/tv/1399", "/tv/{id}"},
		{"/3/tv/1399/credits", "/tv/{id}/credits"},
		{"/3/tv/1399/season/2", "/tv/{id}/season/{n}"},
		{"/3/tv/1399/season/2/credits", "/tv/{id}/season/{n}/credits"},
		{"/3/tv/1399/external_ids", "/tv/{id}/external_ids"},
		{"/3/tv/1399/recommendations", "/tv/{id}/recommendations"},
		{"/3/tv/changes", "/tv/changes"},
		{"/3/person/525", "/person/{id}"},
		{"/3/person/525/tv_credits", "/person/{id}/tv_credits"},
		{"/3/find/123", "/find/{id}"},
		{"/3/find/tt0123456", "/find/{external_id}"},
		{"/3/search/tv", "/search/tv"},
		{"/3/search/movie", "/search/movie"},
		{"/3/search/person", "/search/person"},
		{"/3/configuration", "/configuration"},
		// Future paths fall through.
		{"/3/movie/603", "/unknown"},
		// Story 540: genre catalog sync.
		{"/3/genre/tv/list", "/genre/tv/list"},
		// Edge: bare /3 → "/"  → "/unknown" (no rule matches).
		{"/3", "/unknown"},
		// Edge: no /3 prefix (custom base URL in tests).
		{"/tv/1399", "/tv/{id}"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.themoviedb.org"+tc.raw, nil)
			got := TMDBEndpointFor(req)
			assert.Equal(t, tc.want, got)
		})
	}
}
