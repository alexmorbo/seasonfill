// Package catalog implements the catalog-scope (TMDB) read port for the
// universal-search bounded context (ADR-0024 S1.3).
//
// READ-ONLY — load-bearing divergence from internal/discovery/app.TMDBFallback.
// Discovery stub-upserts every TMDB search result into the local tables and
// enqueues hot enrichment (a write-on-read). This adapter does the OPPOSITE:
// pure read overlay, no DB writes, no enrichment enqueue. Reasons (ADR D6 +
// project_seasonfill_tmdb_sole_truth):
//   - D6: catalog is a lazily-merged READ layer ("не always-sync").
//   - Scale: people=650K, person_credits=6.4M rows — stub-upserting on every
//     debounced keystroke would flood those tables + burn TMDB quota.
//   - A search hit is NOT a statement of library membership (Sonarr/Radarr own
//     membership; TMDB is metadata truth).
//
// Do NOT "helpfully" re-add a StubUpserter/EnrichmentDispatcher here.
package catalog

import (
	"context"
	"log/slog"
	"strings"

	"golang.org/x/sync/errgroup"

	searchapp "github.com/alexmorbo/seasonfill/internal/search/app"
	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// defaultLimitPerGroup caps each group when the caller passes <= 0.
const defaultLimitPerGroup = 20

// TMDBSearchClient is the narrow TMDB surface the adapter needs. Declared here
// (not under internal/shared/clients/tmdb) so the catalog context owns its
// minimal contract — tests pass a fake without an HTTP server. Satisfied by
// *tmdb.Client and by *adapters.TMDBClientHolder (both expose the 4 methods).
type TMDBSearchClient interface {
	SearchTV(ctx context.Context, query, language string, page int) (*tmdb.TVListResponse, error)
	SearchMovie(ctx context.Context, query, language string, page int) (*tmdb.MovieListResponse, error)
	SearchCollection(ctx context.Context, query, language string, page int) (*tmdb.CollectionListResponse, error)
	SearchPerson(ctx context.Context, query, language string, page int) (*tmdb.PersonListResponse, error)
}

// Adapter implements app.CatalogSearchRepository over a TMDBSearchClient.
type Adapter struct {
	tmdb TMDBSearchClient
	log  *slog.Logger
}

// compile-time port satisfaction.
var _ searchapp.CatalogSearchRepository = (*Adapter)(nil)

// NewAdapter binds the adapter. log is required (panics on nil). client MAY be
// nil — a nil client means TMDB is disabled at boot, and SearchCatalog then
// returns an empty result with no error and no log spam (graceful degrade).
func NewAdapter(client TMDBSearchClient, log *slog.Logger) *Adapter {
	if log == nil {
		panic("search catalog adapter: log required")
	}
	return &Adapter{tmdb: client, log: log}
}

// SearchCatalog fans out to the four TMDB search endpoints concurrently, one
// goroutine per requested group. A per-group TMDB failure degrades that group
// to empty (WARN) and never fails the whole call (decision (c)/(a)). Excluded
// groups (types) fire NO TMDB call — saves quota. Each group is page=1, sliced
// to limit. Returns a non-nil error only on context cancellation.
func (a *Adapter) SearchCatalog(ctx context.Context, q, language string, limit int, types searchapp.TypeFilter) (searchdomain.LibrarySearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" || a.tmdb == nil {
		return searchdomain.LibrarySearchResult{}, nil
	}
	if limit <= 0 {
		limit = defaultLimitPerGroup
	}

	var (
		series      []searchdomain.SeriesHit
		movies      []searchdomain.MovieHit
		collections []searchdomain.CollectionHit
		people      []searchdomain.PersonHit
	)

	g, gctx := errgroup.WithContext(ctx)

	if types.Series {
		g.Go(func() error {
			resp, err := a.tmdb.SearchTV(gctx, q, language, 1)
			if err != nil {
				a.warn(gctx, "series", q, language, err)
				return nil
			}
			series = mapSeriesHits(resp, limit)
			return nil
		})
	}
	if types.Movie {
		g.Go(func() error {
			resp, err := a.tmdb.SearchMovie(gctx, q, language, 1)
			if err != nil {
				a.warn(gctx, "movie", q, language, err)
				return nil
			}
			movies = mapMovieHits(resp, limit)
			return nil
		})
	}
	if types.Collection {
		g.Go(func() error {
			resp, err := a.tmdb.SearchCollection(gctx, q, language, 1)
			if err != nil {
				a.warn(gctx, "collection", q, language, err)
				return nil
			}
			collections = mapCollectionHits(resp, limit)
			return nil
		})
	}
	if types.Person {
		g.Go(func() error {
			resp, err := a.tmdb.SearchPerson(gctx, q, language, 1)
			if err != nil {
				a.warn(gctx, "person", q, language, err)
				return nil
			}
			people = mapPersonHits(resp, limit)
			return nil
		})
	}

	// closures never return a non-nil error; Wait() is nil unless the group
	// ctx was cancelled by a caller-cancelled parent ctx.
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return searchdomain.LibrarySearchResult{}, err
	}

	return searchdomain.LibrarySearchResult{
		Series: series, Movies: movies, Collections: collections, People: people,
	}, nil
}

func (a *Adapter) warn(ctx context.Context, group, q, lang string, err error) {
	a.log.WarnContext(ctx, "search.catalog."+group+"_failed",
		slog.String("query", q),
		slog.String("language", lang),
		slog.String("error", err.Error()))
}

// ---- mappers: TMDB entry → searchdomain hit VO, Source=catalog, id=0 ----

func mapSeriesHits(resp *tmdb.TVListResponse, limit int) []searchdomain.SeriesHit {
	if resp == nil {
		return nil
	}
	out := make([]searchdomain.SeriesHit, 0, capHint(len(resp.Results), limit))
	for _, r := range resp.Results {
		if len(out) >= limit {
			break
		}
		if r.ID <= 0 || strings.TrimSpace(r.Name) == "" {
			continue
		}
		id := shareddomain.TMDBID(r.ID)
		out = append(out, searchdomain.SeriesHit{
			TMDBID:       &id,
			Title:        r.Name,
			Year:         yearFromDate(r.FirstAirDate),
			PosterPath:   strPtrOrNil(r.PosterPath),
			BackdropPath: strPtrOrNil(r.BackdropPath),
			Source:       searchdomain.SourceCatalog,
		})
	}
	return out
}

func mapMovieHits(resp *tmdb.MovieListResponse, limit int) []searchdomain.MovieHit {
	if resp == nil {
		return nil
	}
	out := make([]searchdomain.MovieHit, 0, capHint(len(resp.Results), limit))
	for _, r := range resp.Results {
		if len(out) >= limit {
			break
		}
		if r.ID <= 0 || strings.TrimSpace(r.Title) == "" {
			continue
		}
		id := shareddomain.TMDBID(r.ID)
		out = append(out, searchdomain.MovieHit{
			TMDBID:       &id,
			Title:        r.Title,
			Year:         yearFromDate(r.ReleaseDate),
			PosterPath:   strPtrOrNil(r.PosterPath),
			BackdropPath: strPtrOrNil(r.BackdropPath),
			Source:       searchdomain.SourceCatalog,
		})
	}
	return out
}

func mapCollectionHits(resp *tmdb.CollectionListResponse, limit int) []searchdomain.CollectionHit {
	if resp == nil {
		return nil
	}
	out := make([]searchdomain.CollectionHit, 0, capHint(len(resp.Results), limit))
	for _, r := range resp.Results {
		if len(out) >= limit {
			break
		}
		if r.ID <= 0 || strings.TrimSpace(r.Name) == "" {
			continue
		}
		id := shareddomain.TMDBID(r.ID)
		out = append(out, searchdomain.CollectionHit{
			TMDBID:       &id,
			Name:         r.Name,
			PosterPath:   strPtrOrNil(r.PosterPath),
			BackdropPath: strPtrOrNil(r.BackdropPath),
			Source:       searchdomain.SourceCatalog,
		})
	}
	return out
}

func mapPersonHits(resp *tmdb.PersonListResponse, limit int) []searchdomain.PersonHit {
	if resp == nil {
		return nil
	}
	out := make([]searchdomain.PersonHit, 0, capHint(len(resp.Results), limit))
	for _, r := range resp.Results {
		if len(out) >= limit {
			break
		}
		if r.ID <= 0 || strings.TrimSpace(r.Name) == "" {
			continue
		}
		id := shareddomain.TMDBID(r.ID)
		out = append(out, searchdomain.PersonHit{
			TMDBID:      &id,
			Name:        r.Name,
			ProfilePath: strPtrOrNil(r.ProfilePath),
			KnownFor:    strPtrOrNil(r.KnownForDepartment),
			Source:      searchdomain.SourceCatalog,
		})
	}
	return out
}

// capHint bounds the make() hint to min(n, limit) without over-allocating.
func capHint(n, limit int) int {
	if n < limit {
		return n
	}
	return limit
}

// strPtrOrNil returns a *string for a non-empty value, nil otherwise.
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

// yearFromDate extracts YYYY from TMDB's "YYYY-MM-DD". Returns nil for the
// empty/malformed cases (mirror of discovery.yearFromFirstAirDate).
func yearFromDate(s string) *int {
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
