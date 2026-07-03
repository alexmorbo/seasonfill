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
	// S-C: translations → season name/overview for every supported language
	// (episodes[] stays single-lang — see buildSeasonTextWrites O-4).
	// S-C2: images → per-lang season posters; include_image_language is the
	// same UNION (en,ru,null) the all-langs GetTV path uses so ONE GetSeason
	// yields posters for every supported language. Both sub-resources ride the
	// SAME round-trip — no extra TMDB calls.
	q.Set("append_to_response", "translations,images")
	q.Set("include_image_language", includeImageLanguagesAll())

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
