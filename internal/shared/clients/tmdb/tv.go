package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// tvAppendToResponse is the comma-separated list of sub-resources
// the series enrichment worker needs in a single round-trip. PRD
// §5.5 hard-codes this list — adding/removing requires a PRD
// amendment because the TTL matrix assumes one TMDB call per
// series refresh.
const tvAppendToResponse = "aggregate_credits,videos,images,external_ids,content_ratings,keywords,recommendations"

// GetTV fetches /tv/{id} with append_to_response, localised to
// language. The returned *TVResponse is the raw JSON shape; pass
// to MapTVToCanon (and siblings) to extract domain values.
func (c *Client) GetTV(ctx context.Context, id int64, language string) (*TVResponse, error) {
	lang := c.languageFor(language)
	q := url.Values{}
	q.Set("append_to_response", tvAppendToResponse)
	q.Set("language", lang)
	q.Set("include_image_language", includeImageLanguagesFor(lang))

	body, err := c.do(ctx, "/tv/"+strconv.FormatInt(id, 10), q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: GetTV(%d): %w", id, err)
	}
	var out TVResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode TV(%d): %w", id, err)
	}
	return &out, nil
}
