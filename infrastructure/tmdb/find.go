package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// FindByTVDB resolves a TVDB id to a TMDB tv id via the
// /find/{external_id} endpoint. Used by C-2 only when the Sonarr
// series has a tvdb_id but no tmdb_id (legacy library rows).
// Returns the full FindResponse so the caller can also fish a
// movie result out of the same payload (not used today; reserved
// for future).
func (c *Client) FindByTVDB(ctx context.Context, tvdbID int64) (*FindResponse, error) {
	q := url.Values{}
	q.Set("external_source", "tvdb_id")
	q.Set("language", c.lang)

	body, err := c.do(ctx, "/find/"+strconv.FormatInt(tvdbID, 10), q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: FindByTVDB(%d): %w", tvdbID, err)
	}
	var out FindResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode Find(%d): %w", tvdbID, err)
	}
	return &out, nil
}
