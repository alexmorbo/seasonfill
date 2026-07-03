package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// tvAppendToResponse is the comma-separated list of sub-resources
// the series enrichment worker needs in a single round-trip. PRD
// §5.5 hard-codes this list — adding/removing requires a PRD
// amendment because the TTL matrix assumes one TMDB call per
// series refresh.
const tvAppendToResponse = "aggregate_credits,videos,images,external_ids,content_ratings,keywords,recommendations,translations"

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

// GetTVAllLangs fetches /tv/{id} for the S-B all-langs enrichment path in a
// SINGLE round-trip:
//   - language is pinned to the base tag (locale.Default() = en-US) so the
//     ROOT Name/Overview/Tagline arrive in en-US — this guarantees the base
//     series_texts row even when the translations array is empty.
//   - append_to_response carries the translations sub-resource, so every
//     other supported language's localised name/overview/tagline is present.
//   - include_image_language is the UNION of all supported short-langs (+
//     null), so images[] yields per-language posters/backdrops for every
//     supported language at once.
//
// The per-lang GetTV(ctx,id,lang) path is intentionally left unchanged
// (RefreshSeriesText still requests en,null / ru,en,null).
func (c *Client) GetTVAllLangs(ctx context.Context, id int64) (*TVResponse, error) {
	q := url.Values{}
	q.Set("append_to_response", tvAppendToResponse)
	q.Set("language", locale.Default())
	q.Set("include_image_language", includeImageLanguagesAll())

	body, err := c.do(ctx, "/tv/"+strconv.FormatInt(id, 10), q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: GetTVAllLangs(%d): %w", id, err)
	}
	var out TVResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode TVAllLangs(%d): %w", id, err)
	}
	return &out, nil
}
