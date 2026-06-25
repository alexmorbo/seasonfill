// genre.go wraps TMDB /3/genre/tv/list. Used by the discovery
// genre_sync background loop (story 540 / B-49) to keep the canonical
// per-language genre name catalog (genres_i18n) in sync. The catalog
// is small (~18 rows × supported langs) and stable — call cadence at
// the call site is 24h, not per-request.
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// GenreListTV fetches /genre/tv/list?language=<lang>. Empty language
// collapses to c.languageFor("") which yields DefaultLanguage. Returns
// the full TMDB-canonical genre set for that language — caller persists.
//
// TMDB returns roughly 16 entries; the upstream rarely mutates the list
// (last change: 2018).
func (c *Client) GenreListTV(ctx context.Context, language string) (*GenreListResponse, error) {
	q := url.Values{}
	q.Set("language", c.languageFor(language))
	body, err := c.do(ctx, "/genre/tv/list", q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: GenreListTV: %w", err)
	}
	var out GenreListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode GenreListTV: %w", err)
	}
	return &out, nil
}
