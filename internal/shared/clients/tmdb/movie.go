package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// movieAppendToResponse is the comma-separated sub-resource list the movie enrichment worker
// consumes in a single round-trip. external_ids (imdb fallback), release_dates (digital/
// physical), images (per-lang art), translations (localized title/overview/tagline), credits
// (Ф1.1a cast), keywords (Ф1.1b), recommendations + videos (Ф1.1c → movie_recommendations /
// movie_videos best trailer). genres[] and production_companies[] are /movie ROOT fields.
const movieAppendToResponse = "external_ids,release_dates,images,translations,credits,keywords,recommendations,videos"

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

// MapMovieRecommendations flattens recommendations.results[*] into stub movie.Canon rows plus
// a parallel TMDB-rank-order id slice (Ф1.1c). Hydration is HydrationStub — the recs writer
// UpsertStubs these (COALESCE-preserving any existing full hydration) before writing the
// movie_recommendations join. Rows with a zero tmdb id are skipped. Returns (nil, nil) when
// the sub-resource is absent. Mirror of MapTVToRecommendations for the movie payload shape.
func MapMovieRecommendations(m *MovieResponse) ([]movie.Canon, []domain.TMDBID) {
	if m == nil || m.Recommendations == nil {
		return nil, nil
	}
	stubs := make([]movie.Canon, 0, len(m.Recommendations.Results))
	order := make([]domain.TMDBID, 0, len(m.Recommendations.Results))
	for _, r := range m.Recommendations.Results {
		if r.ID == 0 {
			continue
		}
		tid := domain.TMDBID(r.ID)
		c := movie.Canon{
			TMDBID:           &tid,
			Hydration:        movie.HydrationStub,
			Title:            r.Title,
			OriginalTitle:    nonEmptyPtr(r.OriginalTitle),
			OriginalLanguage: nonEmptyPtr(r.OriginalLanguage),
			ReleaseDate:      parseDate(r.ReleaseDate),
			Popularity:       nonZeroFloatPtr(r.Popularity),
			TMDBRating:       nonZeroFloatPtr(r.VoteAverage),
			TMDBVotes:        nonZeroIntPtr(r.VoteCount),
			PosterAsset:      nonEmptyPtr(r.PosterPath),
			BackdropAsset:    nonEmptyPtr(r.BackdropPath),
		}
		if c.ReleaseDate != nil {
			y := c.ReleaseDate.Year()
			c.Year = &y
		}
		stubs = append(stubs, c)
		order = append(order, tid)
	}
	return stubs, order
}

// MapMovieBestTrailer selects the single best trailer from the videos sub-resource and maps
// it to a movie.Video (Ф1.1c). Returns nil when there are no videos or no YouTube candidate.
func MapMovieBestTrailer(m *MovieResponse) *movie.Video {
	if m == nil || m.Videos == nil {
		return nil
	}
	v, ok := pickBestTrailer(m.Videos.Results)
	if !ok {
		return nil
	}
	return &movie.Video{
		TMDBVideoID: nonEmptyPtr(v.ID),
		Name:        v.Name,
		Site:        nonEmptyPtr(v.Site),
		Key:         nonEmptyPtr(v.Key),
		Type:        nonEmptyPtr(v.Type),
		Official:    v.Official,
		Language:    nonEmptyPtr(v.ISO6391),
		PublishedAt: parseRFC3339(v.PublishedAt),
	}
}

// pickBestTrailer returns the best YouTube trailer from a videos slice, deterministically.
// ONLY site==YouTube (case-insensitive) with a non-empty key are candidates — a movie with
// no YouTube video yields (_, false) so the writer clears movie_videos. Ranking (best first):
//  1. official == true beats official == false
//  2. type rank: Trailer < Teaser < Clip < everything else (lower = better)
//  3. newer published_at wins (nil published_at sorts last)
//  4. tie-break: lexicographically smaller tmdb video id (stable determinism)
func pickBestTrailer(vids []TVVideo) (TVVideo, bool) {
	cands := make([]TVVideo, 0, len(vids))
	for _, v := range vids {
		if strings.EqualFold(v.Site, "YouTube") && v.Key != "" {
			cands = append(cands, v)
		}
	}
	if len(cands) == 0 {
		return TVVideo{}, false
	}
	slices.SortStableFunc(cands, func(a, b TVVideo) int {
		if a.Official != b.Official {
			if a.Official {
				return -1
			}
			return 1
		}
		if r := trailerTypeRank(a.Type) - trailerTypeRank(b.Type); r != 0 {
			return r
		}
		if c := compareTimeDescNilLast(parseRFC3339(a.PublishedAt), parseRFC3339(b.PublishedAt)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return cands[0], true
}

// trailerTypeRank orders TMDB video types so a Trailer outranks a Teaser outranks a Clip
// (lower rank = preferred). Case-insensitive; unknown types sort last.
func trailerTypeRank(t string) int {
	switch {
	case strings.EqualFold(t, "Trailer"):
		return 0
	case strings.EqualFold(t, "Teaser"):
		return 1
	case strings.EqualFold(t, "Clip"):
		return 2
	default:
		return 3
	}
}

// compareTimeDescNilLast orders two optional times newest-first with nil last. Returns a
// negative value when a should sort before b (a is newer, or b is nil).
func compareTimeDescNilLast(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case a.After(*b):
		return -1
	case a.Before(*b):
		return 1
	default:
		return 0
	}
}
