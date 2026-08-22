package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	discoverypersistence "github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// discovery_movie.go wires the movie discovery vertical (Ф6-R-4a L3-1). The
// vertical-slice rule (PRD §3.3) forbids internal/discovery/ from importing
// internal/enrichment/ or internal/catalog/ directly; movieStubUpserterAdapter
// lives here (wiring may import every context) to bridge the discovery
// MovieStubUpserter port onto the enrichment MovieRepository COALESCE Upsert +
// the movie_i18n seeder — mirror of stubUpserterAdapter.

// movieStubUpserterAdapter bridges discoapp.MovieStubUpserter →
// enrichpersistence.MovieRepository.Upsert (COALESCE-guarded) + MovieI18nSeeder.
// A discovery stub carries only tmdb_id/title/original_title/original_language/
// poster/backdrop → all enrichment columns stay untouched on a re-materialise
// (movieUpsertAssignments COALESCEs them). The stub sets Hydration=stub so a
// pre-existing 'full' row is never downgraded.
type movieStubUpserterAdapter struct {
	movies *enrichpersistence.MovieRepository
	i18n   *enrichpersistence.MovieI18nSeeder
}

func (a *movieStubUpserterAdapter) EnsureMovieStub(
	ctx context.Context,
	tmdbID shareddomain.TMDBID,
	lang, title, originalTitle, originalLanguage string,
	poster, backdrop *string,
) (shareddomain.MovieID, error) {
	if title == "" {
		return 0, fmt.Errorf("discovery movie stub upserter: title required")
	}
	// Copy tmdbID into a local before taking its address so the pointer in
	// movie.Canon does not alias the caller's parameter slot.
	tid := tmdbID
	c := movie.Canon{
		TMDBID:    &tid,
		Hydration: movie.HydrationStub,
		Title:     title,
	}
	if originalTitle != "" {
		c.OriginalTitle = &originalTitle
	}
	if originalLanguage != "" {
		c.OriginalLanguage = &originalLanguage
	}
	// Raw TMDB paths — the enrichment worker later rewrites poster_asset /
	// backdrop_asset to resolved hashes (COALESCE-preserved). Matches the
	// series stub adapter's raw-vs-hash choice.
	if poster != nil {
		c.PosterAsset = poster
	}
	if backdrop != nil {
		c.BackdropAsset = backdrop
	}
	id, err := a.movies.Upsert(ctx, c)
	if err != nil {
		return 0, fmt.Errorf("ensure movie stub: %w", err)
	}
	if serr := a.i18n.SeedStub(ctx, id, lang, title, poster, backdrop); serr != nil {
		return id, fmt.Errorf("seed movie i18n: %w", serr) // caller logs+drops
	}
	return id, nil
}

// MovieDiscoveryBundle groups the LRU + passthrough + handler wired for the
// movie discovery endpoints (Ф6-R-4a L3-1).
type MovieDiscoveryBundle struct {
	Handler *discoveryrest.MovieDiscoverHandler
	LRU     *cachewatch.Cache[string, []disco.MovieItem]
}

// MovieDiscoveryDeps is the input contract for BuildMovieDiscovery.
type MovieDiscoveryDeps struct {
	Persistence *PersistenceBundle
	// TMDBClient is the live movie list surface — *adapters.TMDBClientHolder
	// (or *tmdb.Client) satisfies it via the four movie methods.
	TMDBClient discoapp.MovieTMDBDiscoverClient
	// Resolver — optional shared *media.Resolver threaded into the handler so
	// movie discovery rewrites raw TMDB paths to sha256 wire hashes. Nil-OK.
	Resolver *media.Resolver
	Log      *slog.Logger
}

// BuildMovieDiscovery wires the movie stub-upserter adapter (over the
// enrichment MovieRepository COALESCE Upsert + movie_i18n seeder), the LRU +
// movie passthrough + handler. LRU sizing mirrors the TV discover cache
// (1000 entries, TTL 1h, ~500 bytes/item).
func BuildMovieDiscovery(deps MovieDiscoveryDeps) *MovieDiscoveryBundle {
	switch {
	case deps.Persistence == nil:
		panic("BuildMovieDiscovery: Persistence required")
	case deps.TMDBClient == nil:
		panic("BuildMovieDiscovery: TMDBClient required")
	case deps.Log == nil:
		panic("BuildMovieDiscovery: Log required")
	}
	stubs := &movieStubUpserterAdapter{
		movies: enrichpersistence.NewMovieRepository(deps.Persistence.DB),
		i18n:   enrichpersistence.NewMovieI18nSeeder(deps.Persistence.DB),
	}
	sizer := func(k string, v []disco.MovieItem) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.MovieItem]("movie_discover", 1000, 1*time.Hour, sizer)
	pass := discoapp.NewMovieTMDBPassthrough(deps.TMDBClient, stubs, deps.Log)
	localSearch := discoverypersistence.NewMovieSearchRepository(deps.Persistence.DB)
	handler := discoveryrest.NewMovieDiscoverHandler(lru, pass, localSearch, deps.Resolver, deps.Log)
	return &MovieDiscoveryBundle{Handler: handler, LRU: lru}
}
