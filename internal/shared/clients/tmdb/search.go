package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// SearchCollection fetches /search/collection?query=… Mirrors SearchTV /
// SearchMovie: trim-empty query → error, language via c.languageFor, page via
// pageOrOne, include_adult hardcoded false (TMDB ignores it on this endpoint —
// kept for uniformity with the other search methods). ADR-0024 S1.3.
func (c *Client) SearchCollection(ctx context.Context, query, language string, page int) (*CollectionListResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("tmdb: SearchCollection: empty query")
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	q.Set("include_adult", "false")

	body, err := c.do(ctx, "/search/collection", q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: SearchCollection: %w", err)
	}
	var out CollectionListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode SearchCollection: %w", err)
	}
	return &out, nil
}

// SearchPerson fetches /search/person?query=… Same mirroring as
// SearchCollection. known_for[] in each result is ignored (v1 scope).
func (c *Client) SearchPerson(ctx context.Context, query, language string, page int) (*PersonListResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("tmdb: SearchPerson: empty query")
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	q.Set("include_adult", "false")

	body, err := c.do(ctx, "/search/person", q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: SearchPerson: %w", err)
	}
	var out PersonListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode SearchPerson: %w", err)
	}
	return &out, nil
}
