package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// GetCollection fetches /collection/{id}, localised to language. Mirrors GetMovie
// (movie.go:37): language-aware via c.languageFor, quota auto-ticked inside c.do,
// error mapping via fmt wrap. The returned *CollectionResponse is the raw JSON
// shape; pass to MapCollectionToCanon (collection row) and MapCollectionPartsToCanon
// (member-movie stubs).
func (c *Client) GetCollection(ctx context.Context, id int64, language string) (*CollectionResponse, error) {
	lang := c.languageFor(language)
	q := url.Values{}
	q.Set("language", lang)

	body, err := c.do(ctx, "/collection/"+strconv.FormatInt(id, 10), q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: GetCollection(%d): %w", id, err)
	}
	var out CollectionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode Collection(%d): %w", id, err)
	}
	return &out, nil
}

// MapCollectionToCanon flattens a CollectionResponse into a movie.CollectionCanon.
// Poster/backdrop are copied as RAW TMDB paths into *_asset (mirror of
// MapMovieToCanon's poster/backdrop → *_asset convention). Monitored /
// RadarrMonitored are LEFT ZERO — they are operator/Radarr-owned and the
// COALESCE UpsertCollection never writes them.
func MapCollectionToCanon(r *CollectionResponse) movie.CollectionCanon {
	if r == nil {
		return movie.CollectionCanon{}
	}
	return movie.CollectionCanon{
		TMDBCollectionID: int(r.ID),
		Name:             r.Name,
		Overview:         nonEmptyPtr(r.Overview),
		PosterAsset:      nonEmptyPtr(r.PosterPath),
		BackdropAsset:    nonEmptyPtr(r.BackdropPath),
	}
}

// MapCollectionPartsToCanon turns each part into a STUB (hydration=stub) movie
// canon carrying tmdb_id + collection_id, so the COALESCE MovieRepository.Upsert
// links the part into the franchise (movies.collection_id == r.ID) without
// blanking any fuller enrichment a later /movie/{id} hydrate wrote. Parts with a
// zero id are skipped. Returns nil for a nil / empty response.
func MapCollectionPartsToCanon(r *CollectionResponse) []movie.Canon {
	if r == nil || len(r.Parts) == 0 {
		return nil
	}
	cid := int(r.ID)
	out := make([]movie.Canon, 0, len(r.Parts))
	for _, p := range r.Parts {
		if p.ID == 0 {
			continue
		}
		tid := domain.TMDBID(p.ID)
		linked := cid
		c := movie.Canon{
			TMDBID:           &tid,
			Hydration:        movie.HydrationStub,
			Title:            p.Title,
			OriginalTitle:    nonEmptyPtr(p.OriginalTitle),
			OriginalLanguage: nonEmptyPtr(p.OriginalLanguage),
			ReleaseDate:      parseDate(p.ReleaseDate),
			CollectionID:     &linked,
			PosterAsset:      nonEmptyPtr(p.PosterPath),
			BackdropAsset:    nonEmptyPtr(p.BackdropPath),
			Popularity:       nonZeroFloatPtr(p.Popularity),
			TMDBRating:       nonZeroFloatPtr(p.VoteAverage),
			TMDBVotes:        nonZeroIntPtr(p.VoteCount),
		}
		if c.ReleaseDate != nil {
			y := c.ReleaseDate.Year()
			c.Year = &y
		}
		out = append(out, c)
	}
	return out
}
