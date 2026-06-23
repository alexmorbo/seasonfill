// search.go ships the discovery search use case (story 508 N-2g).
// Two-tier lookup per PRD §5.1.1 lines 711-720:
//
//  1. Local LIKE over series.title (and series_texts.title for non-en
//     preferred languages) ranked by popularity DESC NULLS LAST,
//     tmdb_rating DESC NULLS LAST.
//  2. On local miss, fall back to TMDB /search/tv, stub-upsert each
//     result via the StubUpserter port (story 505), enqueue each new
//     stub for hot enrichment so the next Series Detail paint is rich.
//
// Portable SQL only — LIKE + LOWER + NULLS LAST. No ILIKE / pg_trgm /
// tsvector. Both Postgres and SQLite share the implementation.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SearchRepo is the local catalog read surface for the search fallback.
// Implementation: internal/discovery/persistence.SearchRepository.
type SearchRepo interface {
	LocalSearch(ctx context.Context, query, language string, limit int) ([]disco.Item, error)
}

// SearchTMDB is the narrow TMDB port the fallback hits. *tmdb.Client
// satisfies it directly via Client.SearchTV. Declared here (not under
// internal/shared/clients/tmdb) so the discovery context owns its own
// minimal contract — handler tests pass a fake without an HTTP server.
type SearchTMDB interface {
	SearchTV(ctx context.Context, query, language string, page int) (*tmdb.TVListResponse, error)
}

// EnrichmentDispatcher is the narrow enqueue port for the post-stub
// hot-enrichment kick. *appenrich.DispatcherImpl satisfies it via
// Enqueue(EntitySeries, id, PriorityHot). Wiring wraps the concrete
// dispatcher in an adapter so discovery never imports enrichment/app.
// The entity / priority strings ARE the appenrich constants — kept as
// strings here for import isolation.
type EnrichmentDispatcher interface {
	Enqueue(entity string, id int64, priority string)
}

// EntitySeriesKind / PriorityHotLabel are the wire constants the
// EnrichmentDispatcher adapter translates. The wiring adapter validates
// these against the appenrich enum at construction.
const (
	EntitySeriesKind = "series"
	PriorityHotLabel = "hot"
)

// SearchUseCase is the orchestrator. Constructed once at boot via
// internal/wiring/discovery.go and held on the DiscoveryHandler.
type SearchUseCase struct {
	repo     SearchRepo
	tmdb     SearchTMDB
	stubs    StubUpserter
	dispatch EnrichmentDispatcher
	log      *slog.Logger
}

// NewSearchUseCase wires the use case against its narrow ports. Every
// arg is required — panics on nil so a wiring bug surfaces at startup.
// log MUST already carry a domain tag (production wiring passes
// sharedports.DomainLogger(log, "discovery")).
func NewSearchUseCase(
	repo SearchRepo,
	tmdbClient SearchTMDB,
	stubs StubUpserter,
	dispatch EnrichmentDispatcher,
	log *slog.Logger,
) *SearchUseCase {
	switch {
	case repo == nil:
		panic("search use case: repo required")
	case tmdbClient == nil:
		panic("search use case: tmdb required")
	case stubs == nil:
		panic("search use case: stubs required")
	case dispatch == nil:
		panic("search use case: dispatch required")
	case log == nil:
		panic("search use case: log required")
	}
	return &SearchUseCase{
		repo:     repo,
		tmdb:     tmdbClient,
		stubs:    stubs,
		dispatch: dispatch,
		log:      log,
	}
}

// ErrTMDBUnavailable signals the TMDB fallback could not complete.
// The handler maps this to 502.
var ErrTMDBUnavailable = errors.New("discovery search: tmdb unavailable")

// Local runs the local LIKE lookup. Returns at most `limit` items
// ranked by popularity DESC NULLS LAST, tmdb_rating DESC NULLS LAST.
// A nil/empty result is NOT an error — the handler distinguishes
// len(items)==0 to trigger the TMDB fallback.
func (uc *SearchUseCase) Local(ctx context.Context, q, language string, limit int) ([]disco.Item, error) {
	if limit <= 0 {
		limit = 20
	}
	items, err := uc.repo.LocalSearch(ctx, q, language, limit)
	if err != nil {
		return nil, fmt.Errorf("local search: %w", err)
	}
	return items, nil
}

// TMDBFallback hits /search/tv on page=1 and stub-upserts every result
// into the local series table (hydration='stub'). Each fresh stub is
// enqueued at PriorityHot so the next Series Detail render lands on
// hydrated data. Returns the mapped disco.Item slice — same shape the
// local path returns.
//
// Dispatcher errors are logged at WARN and do NOT fail the fallback —
// a missing enqueue degrades to "stale first paint" but the search
// result is still returned to the user.
func (uc *SearchUseCase) TMDBFallback(ctx context.Context, q, language string) ([]disco.Item, error) {
	resp, err := uc.tmdb.SearchTV(ctx, q, language, 1)
	if err != nil {
		uc.log.WarnContext(ctx, "discovery.search.tmdb_failed",
			slog.String("query", q),
			slog.String("language", language),
			slog.String("error", err.Error()))
		return nil, fmt.Errorf("%w: %s", ErrTMDBUnavailable, err.Error())
	}
	if resp == nil || len(resp.Results) == 0 {
		uc.log.InfoContext(ctx, "discovery.search.tmdb_empty",
			slog.String("query", q),
			slog.String("language", language))
		return nil, nil
	}

	out := make([]disco.Item, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.ID <= 0 || r.Name == "" {
			continue
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
		seriesID, serr := uc.stubs.EnsureStub(ctx, tmdbID, r.Name, poster, backdrop)
		if serr != nil {
			uc.log.WarnContext(ctx, "discovery.search.stub_upsert_failed",
				slog.Int64("tmdb_id", int64(tmdbID)),
				slog.String("title", r.Name),
				slog.String("error", serr.Error()))
			continue
		}
		uc.dispatch.Enqueue(EntitySeriesKind, int64(seriesID), PriorityHotLabel)

		item := disco.Item{
			SeriesID: seriesID,
			TMDBID:   &tmdbID,
			Title:    r.Name,
		}
		if r.PosterPath != "" {
			pp := r.PosterPath
			item.PosterPath = &pp
		}
		if r.BackdropPath != "" {
			bp := r.BackdropPath
			item.BackdropPath = &bp
		}
		if y := yearFromFirstAirDate(r.FirstAirDate); y != nil {
			item.Year = y
		}
		if len(r.OriginCountry) > 0 {
			item.OriginCountries = append([]string(nil), r.OriginCountry...)
		}
		out = append(out, item)
	}
	uc.log.InfoContext(ctx, "discovery.search.tmdb_fallback",
		slog.String("query", q),
		slog.String("language", language),
		slog.Int("results", len(out)))
	return out, nil
}

// yearFromFirstAirDate extracts YYYY from TMDB's "YYYY-MM-DD" string.
// Returns nil for the empty / malformed cases — Item.Year is *int so
// "no year" stays nil rather than zeroing the wire field.
func yearFromFirstAirDate(s string) *int {
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
