package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// personAppendToResponse mirrors PRD §5.5: one /person/{id} call
// returns biography + filmography + external ids in a single
// round-trip.
const personAppendToResponse = "tv_credits,movie_credits,external_ids"

// GetPerson fetches /person/{id} with append_to_response.
func (c *Client) GetPerson(ctx context.Context, id int64, language string) (*PersonResponse, error) {
	lang := c.languageFor(language)
	q := url.Values{}
	q.Set("append_to_response", personAppendToResponse)
	q.Set("language", lang)

	body, err := c.do(ctx, "/person/"+strconv.FormatInt(id, 10), q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: GetPerson(%d): %w", id, err)
	}
	var out PersonResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode Person(%d): %w", id, err)
	}
	return &out, nil
}
