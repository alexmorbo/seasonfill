// movie_passthrough.go ships the TMDB ad-hoc fetch adapter for the movie
// discovery handlers (Ф6-R-4a L3-1). Structural mirror of passthrough.go for
// the movie vertical: each of the four movie list endpoints
// (discover/trending/popular/search) is fetched under the request language,
// every returned row is stub-upserted into the `movies` canon, and a single
// WARN-and-drop covers a stub failure so one bad row never fails the whole
// response.
//
// The narrow ports (MovieTMDBDiscoverClient, MovieStubUpserter) let handler
// tests pass fakes without an httptest server. *tmdb.Client satisfies the
// client port via duck typing.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieTMDBPassthrough is the port the movie discovery handler reads through.
// The four Fetch* methods perform one TMDB call each + map results to
// disco.MovieItem, stub-upserting unknown TMDB ids as a side effect.
// LastWaitSeconds reports the most recent Fetch wall-clock so the handler can
// fold the `tmdb_throttled` degraded signal.
type MovieTMDBPassthrough interface {
	FetchDiscover(ctx context.Context, filter tmdb.MovieDiscoverFilter, lang string, page int) ([]disco.MovieItem, error)
	FetchTrending(ctx context.Context, scope tmdb.TrendingScope, lang string, page int) ([]disco.MovieItem, error)
	FetchPopular(ctx context.Context, lang string, page int) ([]disco.MovieItem, error)
	FetchSearch(ctx context.Context, query, lang string, page int) ([]disco.MovieItem, error)
	LastWaitSeconds() float64
}

// ErrMovieTMDBUnavailable signals the upstream call failed (network, 5xx,
// decode error). The handler maps it to 502 tmdb_unavailable.
var ErrMovieTMDBUnavailable = errors.New("discovery movie: tmdb unavailable")

// movieTMDBPassthroughAdapter is the concrete MovieTMDBPassthrough wired in
// wiring/discovery_movie.go. Construct via NewMovieTMDBPassthrough.
type movieTMDBPassthroughAdapter struct {
	tmdb  MovieTMDBDiscoverClient
	stubs MovieStubUpserter
	log   *slog.Logger
	// lastWaitNanos is the wall-clock around the most recent Fetch. Updated
	// under atomic so a parallel handler reader doesn't race the writer.
	lastWaitNanos atomic.Int64
}

// NewMovieTMDBPassthrough wires the passthrough against its narrow ports.
// Every arg is required — panics on nil so a wiring bug surfaces at boot.
// log MUST already carry the "discovery" domain tag.
func NewMovieTMDBPassthrough(client MovieTMDBDiscoverClient, stubs MovieStubUpserter, log *slog.Logger) *movieTMDBPassthroughAdapter {
	switch {
	case client == nil:
		panic("discovery movie passthrough: tmdb client required")
	case stubs == nil:
		panic("discovery movie passthrough: stubs required")
	case log == nil:
		panic("discovery movie passthrough: log required")
	}
	return &movieTMDBPassthroughAdapter{tmdb: client, stubs: stubs, log: log}
}

// FetchDiscover performs one /discover/movie call under lang + materialises
// every returned movie id as a local stub.
func (a *movieTMDBPassthroughAdapter) FetchDiscover(ctx context.Context, filter tmdb.MovieDiscoverFilter, lang string, page int) ([]disco.MovieItem, error) {
	start := time.Now()
	resp, err := a.tmdb.DiscoverMovie(ctx, filter, lang, page)
	return a.finish(ctx, "discover", lang, page, resp, err, start)
}

// FetchTrending performs one /trending/movie/{scope} call under lang.
func (a *movieTMDBPassthroughAdapter) FetchTrending(ctx context.Context, scope tmdb.TrendingScope, lang string, page int) ([]disco.MovieItem, error) {
	start := time.Now()
	resp, err := a.tmdb.TrendingMovie(ctx, scope, lang, page)
	return a.finish(ctx, "trending", lang, page, resp, err, start)
}

// FetchPopular performs one /movie/popular call under lang.
func (a *movieTMDBPassthroughAdapter) FetchPopular(ctx context.Context, lang string, page int) ([]disco.MovieItem, error) {
	start := time.Now()
	resp, err := a.tmdb.MoviePopular(ctx, lang, page)
	return a.finish(ctx, "popular", lang, page, resp, err, start)
}

// FetchSearch performs one /search/movie call under lang.
func (a *movieTMDBPassthroughAdapter) FetchSearch(ctx context.Context, query, lang string, page int) ([]disco.MovieItem, error) {
	start := time.Now()
	resp, err := a.tmdb.SearchMovie(ctx, query, lang, page)
	return a.finish(ctx, "search", lang, page, resp, err, start)
}

// finish records the wall-clock, wraps a transport error, and maps a
// successful response into stub-upserted disco.MovieItem rows. Shared tail of
// the four Fetch* methods so wall-clock + error mapping + logging stay
// byte-identical across endpoints.
func (a *movieTMDBPassthroughAdapter) finish(
	ctx context.Context,
	endpoint, lang string,
	page int,
	resp *tmdb.MovieListResponse,
	err error,
	start time.Time,
) ([]disco.MovieItem, error) {
	wait := time.Since(start)
	a.lastWaitNanos.Store(wait.Nanoseconds())

	if err != nil {
		a.log.WarnContext(ctx, "discovery.movie.tmdb_failed",
			slog.String("endpoint", endpoint),
			slog.Int("page", page),
			slog.Float64("wait_seconds", wait.Seconds()),
			slog.String("error", err.Error()))
		return nil, fmt.Errorf("%w: %s", ErrMovieTMDBUnavailable, err.Error())
	}
	if resp == nil || len(resp.Results) == 0 {
		return nil, nil
	}

	out := make([]disco.MovieItem, 0, len(resp.Results))
	for _, r := range resp.Results {
		it, ok := a.materialiseMovieEntry(ctx, lang, r)
		if !ok {
			continue
		}
		out = append(out, it)
	}
	a.log.InfoContext(ctx, "discovery.movie.fetched",
		slog.String("endpoint", endpoint),
		slog.Int("page", page),
		slog.Int("results", len(out)),
		slog.Float64("wait_seconds", wait.Seconds()))
	return out, nil
}

// LastWaitSeconds reports the wall-clock spent inside the most recent Fetch.
// Returns 0 before the first call. Read by the handler to size the
// `tmdb_throttled` degraded signal — over 1s flips the flag.
func (a *movieTMDBPassthroughAdapter) LastWaitSeconds() float64 {
	n := a.lastWaitNanos.Load()
	if n <= 0 {
		return 0
	}
	return float64(n) / float64(time.Second)
}

// materialiseMovieEntry mirrors passthrough.go materialiseEntry for the movie
// vertical. Stub-upsert errors are logged at WARN and the entry dropped; a
// single bad row never fails the whole response. Does NOT enqueue for hot
// enrichment — Discover is an exploration surface; the movie refresh scheduler
// (L3-2) picks up the new stub.
//
// The movie is stub-upserted under the request lang so movie_i18n{lang} is
// seeded in the language the row was fetched (mirror of the series seeding;
// issue #1184 — the request lang, never the client default).
func (a *movieTMDBPassthroughAdapter) materialiseMovieEntry(ctx context.Context, lang string, r tmdb.MovieListEntry) (disco.MovieItem, bool) {
	if r.ID <= 0 || r.Title == "" {
		return disco.MovieItem{}, false
	}
	tmdbID := shareddomain.TMDBID(r.ID)
	var poster, backdrop *string
	if r.PosterPath != "" {
		v := r.PosterPath
		poster = &v
	}
	if r.BackdropPath != "" {
		v := r.BackdropPath
		backdrop = &v
	}
	movieID, err := a.stubs.EnsureMovieStub(ctx, tmdbID, lang, r.Title, r.OriginalTitle, r.OriginalLanguage, poster, backdrop)
	if err != nil {
		a.log.WarnContext(ctx, "discovery.movie.stub_upsert_failed",
			slog.Int64("tmdb_id", int64(tmdbID)),
			slog.String("title", r.Title),
			slog.String("error", err.Error()))
		return disco.MovieItem{}, false
	}
	item := disco.MovieItem{
		MovieID:      movieID,
		TMDBID:       &tmdbID,
		Title:        r.Title,
		PosterPath:   poster,
		BackdropPath: backdrop,
	}
	if y := yearFromReleaseDate(r.ReleaseDate); y != nil {
		item.Year = y
	}
	if r.VoteAverage > 0 {
		v := r.VoteAverage
		item.TMDBRating = &v
	}
	if r.OriginalLanguage != "" {
		ol := r.OriginalLanguage
		item.OriginalLanguage = &ol
	}
	return item, true
}

// yearFromReleaseDate extracts YYYY from TMDB's "YYYY-MM-DD" release_date.
// Movie sibling of yearFromFirstAirDate — returns nil for empty / malformed
// input so MovieItem.Year stays nil rather than zeroing the wire field.
func yearFromReleaseDate(s string) *int {
	if len(s) < 4 {
		return nil
	}
	y := 0
	for i := range 4 {
		c := s[i]
		if c < '0' || c > '9' {
			return nil
		}
		y = y*10 + int(c-'0')
	}
	if y < 1800 || y > 9999 {
		return nil
	}
	return &y
}
