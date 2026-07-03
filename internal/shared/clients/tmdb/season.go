package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// GetSeason fetches /tv/{id}/season/{n} localised to language.
// Returns the raw SeasonResponse; MapSeasonToEpisodes /
// MapSeasonToCredits do the domain conversion.
func (c *Client) GetSeason(ctx context.Context, tvID int64, seasonNumber int, language string) (*SeasonResponse, error) {
	lang := c.languageFor(language)
	q := url.Values{}
	q.Set("language", lang)
	// S-C: pull the translations sub-resource so ONE GetSeason yields the
	// season name/overview for EVERY supported language (episodes[] stays
	// single-lang — see buildSeasonTextWrites O-4 note). No `images` here —
	// per-lang season art is S-C2.
	q.Set("append_to_response", "translations")

	path := "/tv/" + strconv.FormatInt(tvID, 10) + "/season/" + strconv.Itoa(seasonNumber)
	body, err := c.do(ctx, path, q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: GetSeason(%d, %d): %w", tvID, seasonNumber, err)
	}
	var out SeasonResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode Season(%d, %d): %w", tvID, seasonNumber, err)
	}
	return &out, nil
}
