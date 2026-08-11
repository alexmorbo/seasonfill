package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// movieAppendToResponse is the comma-separated sub-resource list the movie
// enrichment worker consumes in a single round-trip (Ф6-R-4a L3-2). Trimmed to
// exactly what MapMovieToCanon + the worker's i18n write read: external_ids
// (imdb fallback), release_dates (digital/physical), images (per-lang art),
// translations (localized title/overview/tagline). credits/keywords/videos/
// recommendations are deferred to R-4b — keeping this list minimal keeps the
// payload small (movie analog of tvAppendToResponse; do NOT touch tv.go).
const movieAppendToResponse = "external_ids,release_dates,images,translations"

// releaseTypeDigital / releaseTypePhysical are the TMDB release_dates type enum
// values the mapper extracts into movies.digital_release_date /
// physical_release_date.
const (
	releaseTypeDigital  = 4
	releaseTypePhysical = 5
)

// GetMovie fetches /movie/{id} with append_to_response, localised to language.
// Language-aware via c.languageFor(language) exactly like GetTV (issue #1184 —
// a non-default-lang request localises title/overview/art). The returned
// *MovieResponse is the raw JSON shape; pass to MapMovieToCanon to extract
// canon domain values.
func (c *Client) GetMovie(ctx context.Context, id int64, language string) (*MovieResponse, error) {
	lang := c.languageFor(language)
	q := url.Values{}
	q.Set("append_to_response", movieAppendToResponse)
	q.Set("language", lang)
	q.Set("include_image_language", includeImageLanguagesFor(lang))

	body, err := c.do(ctx, "/movie/"+strconv.FormatInt(id, 10), q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: GetMovie(%d): %w", id, err)
	}
	var out MovieResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode Movie(%d): %w", id, err)
	}
	return &out, nil
}

// MapMovieToCanon flattens a MovieResponse into a movie.Canon row.
// Hydration is HydrationFull (the call fetched the full append). Enrichment-
// only columns owned by OMDb (imdb_rating/imdb_votes/omdb_rated/omdb_awards)
// are deliberately LEFT NIL here so the COALESCE Upsert preserves any OMDb
// values written by L3-3. tmdb_changed_at is NOT set — it is written solely by
// the movie changes marker. Localized title/overview/tagline are NOT canon
// fields; the worker writes them to movie_i18n separately.
//
// Time fields are parsed lenient: an empty string yields nil.
func MapMovieToCanon(m *MovieResponse) movie.Canon {
	if m == nil {
		return movie.Canon{}
	}
	tid := domain.TMDBID(m.ID)
	c := movie.Canon{
		TMDBID:           &tid,
		Hydration:        movie.HydrationFull,
		Title:            m.Title,
		OriginalTitle:    nonEmptyPtr(m.OriginalTitle),
		Status:           nonEmptyPtr(m.Status),
		ReleaseDate:      parseDate(m.ReleaseDate),
		Homepage:         nonEmptyPtr(m.Homepage),
		OriginalLanguage: nonEmptyPtr(m.OriginalLanguage),
		Popularity:       nonZeroFloatPtr(m.Popularity),
		TMDBRating:       nonZeroFloatPtr(m.VoteAverage),
		TMDBVotes:        nonZeroIntPtr(m.VoteCount),
		PosterAsset:      nonEmptyPtr(m.PosterPath),
		BackdropAsset:    nonEmptyPtr(m.BackdropPath),
	}
	if c.ReleaseDate != nil {
		y := c.ReleaseDate.Year()
		c.Year = &y
	}
	if m.Runtime > 0 {
		r := m.Runtime
		c.RuntimeMinutes = &r
	}
	if m.Budget > 0 {
		b := m.Budget
		c.Budget = &b
	}
	if m.Revenue > 0 {
		rev := m.Revenue
		c.Revenue = &rev
	}
	if m.BelongsToCollection != nil && m.BelongsToCollection.ID != 0 {
		cid := m.BelongsToCollection.ID
		c.CollectionID = &cid
	}
	c.OriginCountries = movieOriginCountries(m)
	// imdb_id: /movie/{id} carries it at top level; external_ids is a defensive
	// fallback (mirror MapTVToCanon's external_ids read).
	rawIMDB := m.IMDBID
	if rawIMDB == "" && m.ExternalIDs != nil {
		rawIMDB = m.ExternalIDs.IMDBID
	}
	if id := NormaliseIMDBID(rawIMDB); id != "" {
		v := domain.IMDBID(id)
		c.IMDBID = &v
	}
	// digital / physical release dates from the typed release_dates embed.
	if m.ReleaseDates != nil {
		if d := pickReleaseDate(m.ReleaseDates, releaseTypeDigital); d != nil {
			c.DigitalReleaseDate = d
		}
		if p := pickReleaseDate(m.ReleaseDates, releaseTypePhysical); p != nil {
			c.PhysicalReleaseDate = p
		}
	}
	return c
}

// movieOriginCountries prefers the top-level origin_country array; when empty it
// falls back to the production_countries iso codes. Returns nil when both are
// empty (the COALESCE Upsert then preserves any prior value via NULLIF('[]')).
func movieOriginCountries(m *MovieResponse) []string {
	if len(m.OriginCountry) > 0 {
		return append([]string(nil), m.OriginCountry...)
	}
	if len(m.ProductionCountries) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.ProductionCountries))
	for _, pc := range m.ProductionCountries {
		if pc.ISO31661 != "" {
			out = append(out, pc.ISO31661)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// pickReleaseDate scans the release_dates embed for the first entry of the given
// type, preferring the US region for determinism, then falling back to the first
// matching entry in any region. Returns nil when no matching typed row exists.
func pickReleaseDate(rd *MovieReleaseDates, wantType int) *time.Time {
	var fallback *time.Time
	for _, country := range rd.Results {
		for _, e := range country.ReleaseDates {
			if e.Type != wantType {
				continue
			}
			parsed := parseRFC3339(e.ReleaseDate)
			if parsed == nil {
				continue
			}
			if country.ISO31661 == "US" {
				return parsed
			}
			if fallback == nil {
				fallback = parsed
			}
		}
	}
	return fallback
}
