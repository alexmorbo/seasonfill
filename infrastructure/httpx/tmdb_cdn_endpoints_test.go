package httpx

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTMDBCDNEndpointFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{"/t/p/w500/abc123.jpg", "/t/p/w500"},
		{"/t/p/w780/xyz789.png", "/t/p/w780"},
		{"/t/p/original/aaa.jpg", "/t/p/original"},
		{"/t/p/w92/zzz.svg", "/t/p/w92"},
		{"/t/p/w154/q.jpg", "/t/p/w154"},
		{"/t/p/w185/q.jpg", "/t/p/w185"},
		{"/t/p/w300/q.jpg", "/t/p/w300"},
		{"/t/p/w342/q.jpg", "/t/p/w342"},
		{"/t/p/w1280/q.jpg", "/t/p/w1280"},
		// Future/unknown size.
		{"/t/p/w9999/abc.jpg", "/unknown"},
		// Malformed.
		{"/t/p/w500/", "/unknown"},
		{"/anything-else", "/unknown"},
		{"/", "/unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://image.tmdb.org"+tc.raw, nil)
			got := TMDBCDNEndpointFor(req)
			assert.Equal(t, tc.want, got)
		})
	}
}
