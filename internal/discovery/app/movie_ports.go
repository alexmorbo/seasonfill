package app

import (
	"context"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieTMDBDiscoverClient is the narrow read surface the movie passthrough
// hits. *tmdb.Client satisfies it via the four movie list methods. Discovery
// owns this contract (mirror of TMDBDiscoverClient) so handler tests build a
// fake without an httptest server. Every method honors the request language
// via c.languageFor (issue #1184).
type MovieTMDBDiscoverClient interface {
	DiscoverMovie(ctx context.Context, filter tmdb.MovieDiscoverFilter, lang string, page int) (*tmdb.MovieListResponse, error)
	TrendingMovie(ctx context.Context, scope tmdb.TrendingScope, language string, page int) (*tmdb.MovieListResponse, error)
	MoviePopular(ctx context.Context, language string, page int) (*tmdb.MovieListResponse, error)
	SearchMovie(ctx context.Context, query, language string, page int) (*tmdb.MovieListResponse, error)
}

// MovieStubUpserter materialises an unknown TMDB movie into the `movies`
// canon + seeds movie_i18n{lang}. Movie analog of StubUpserter.EnsureStub.
//
// lang is the CALL language the title arrived in. The adapter seeds
// movie_i18n{lang} only-if-absent (never poisoning an enriched base-lang
// title) and writes through R-3's COALESCE MovieRepository.Upsert so no
// enrichment column is nulled on a re-materialise.
//
// Impl: a movieStubUpserterAdapter in internal/wiring/discovery_movie.go that
// wraps enrichment/persistence.MovieRepository.Upsert + the movie_i18n seeder
// — so discovery never imports enrichment/catalog.
type MovieStubUpserter interface {
	EnsureMovieStub(ctx context.Context, tmdbID shareddomain.TMDBID, lang, title, originalTitle, originalLanguage string, poster, backdrop *string) (shareddomain.MovieID, error)
}

// MovieSearchRepo is the local-first read surface for GET /discovery/movie/search
// (ADR-0024 Ф0 S0.2). LocalSearch matches the local movies canon over
// canon title ∪ original_title ∪ movie_i18n and resolves the DISPLAYED title
// to the requested language (→ en-US → canon m.title fallback), so a localized
// (e.g. ru) title resolves before the original-title-biased TMDB /search/movie.
// Impl: internal/discovery/persistence.MovieSearchRepository. Portable SQL
// (LOWER + LIKE + NULLS LAST) — Postgres + SQLite share one plan.
type MovieSearchRepo interface {
	LocalSearch(ctx context.Context, query, language string, limit int) ([]disco.MovieItem, error)
}
