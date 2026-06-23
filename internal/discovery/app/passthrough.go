// passthrough.go ships the TMDB ad-hoc fetch adapter for the
// /discovery/discover handler (story 509 N-2h). Pattern B handler asks
// the passthrough for a single (filter, lang, page) tuple; the adapter
//
//  1. calls tmdb.DiscoverTV with the rate-limiter wait counted against
//     wall-clock so LastWaitSeconds reports the upstream throttle pain
//     to the handler (drives the `tmdb_throttled` degraded signal),
//  2. maps each TVListEntry → disco.Item with the stub-upsert side
//     effect (mirrors the worker's materialiseItem + search use case's
//     fallback mapper — see internal/discovery/app/search.go:128),
//  3. logs a single WARN line per stub-upsert failure but continues —
//     the response still surfaces the items, the missing local row
//     just delays Series Detail richness until the next scan picks
//     up the TMDB id.
//
// The narrow port (TMDBDiscoverClient) lets handler tests pass a fake
// without spinning httptest. Story 504's *tmdb.Client satisfies it via
// duck typing.
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

// TMDBDiscoverClient is the narrow read surface the passthrough hits.
// *tmdb.Client satisfies it via Client.DiscoverTV. Discovery context
// owns this contract (mirrors SearchTMDB in search.go) so tests build a
// fake without an httptest server.
//
// Note: tmdb.Client does NOT yet expose a LastWaitDuration accessor —
// the adapter measures wall-clock around its DiscoverTV call itself.
type TMDBDiscoverClient interface {
	DiscoverTV(ctx context.Context, filter tmdb.DiscoverFilter, page int) (*tmdb.TVListResponse, error)
}

// TMDBPassthrough is the port the handler reads through. Two methods:
//
//   - Fetch performs the single TMDB call + maps results to disco.Item,
//     stub-upserting unknown TMDB ids as a side effect.
//   - LastWaitSeconds reports the most recent Fetch wall-clock (limiter
//     wait + HTTP round trip). The handler folds it into the `degraded`
//     envelope: > 1s → append "tmdb_throttled".
//
// The interface stays narrow so handler tests pass a scripted fake. The
// concrete tmdbPassthroughAdapter ships in this file.
type TMDBPassthrough interface {
	Fetch(ctx context.Context, filter tmdb.DiscoverFilter, lang string, page int) ([]disco.Item, error)
	LastWaitSeconds() float64
}

// ErrTMDBDiscoverUnavailable signals the upstream call failed (network,
// 5xx, decode error). The handler maps it to 502 tmdb_unavailable.
var ErrTMDBDiscoverUnavailable = errors.New("discovery discover: tmdb unavailable")

// tmdbPassthroughAdapter is the concrete TMDBPassthrough wired in
// wiring/discovery.go. Construct via NewTMDBPassthrough.
type tmdbPassthroughAdapter struct {
	tmdb  TMDBDiscoverClient
	stubs StubUpserter
	log   *slog.Logger
	// lastWaitNanos is the wall-clock around the most recent successful
	// or failed Fetch. Updated under atomic so a parallel reader from
	// the handler doesn't race the writer goroutine.
	lastWaitNanos atomic.Int64
}

// NewTMDBPassthrough wires the passthrough against its narrow ports.
// Every arg is required — panics on nil so a wiring bug surfaces at boot.
// log MUST already carry the "discovery" domain tag.
func NewTMDBPassthrough(client TMDBDiscoverClient, stubs StubUpserter, log *slog.Logger) *tmdbPassthroughAdapter {
	switch {
	case client == nil:
		panic("discovery passthrough: tmdb client required")
	case stubs == nil:
		panic("discovery passthrough: stubs required")
	case log == nil:
		panic("discovery passthrough: log required")
	}
	return &tmdbPassthroughAdapter{tmdb: client, stubs: stubs, log: log}
}

// Fetch performs the single /discover/tv call + materialises every
// returned TV id as a local stub. Note: the lang argument is preserved
// for the canonical-cache-key parity but the underlying tmdb.Client
// always uses its own default language (PRD §5.1.2 — Discover stays on
// the client default). Wiring tests assert this.
func (a *tmdbPassthroughAdapter) Fetch(
	ctx context.Context,
	filter tmdb.DiscoverFilter,
	lang string,
	page int,
) ([]disco.Item, error) {
	_ = lang // canonical key already captured lang; tmdb.Client default wins on wire.

	start := time.Now()
	resp, err := a.tmdb.DiscoverTV(ctx, filter, page)
	wait := time.Since(start)
	a.lastWaitNanos.Store(wait.Nanoseconds())

	if err != nil {
		a.log.WarnContext(ctx, "discovery.discover.tmdb_failed",
			slog.Int("page", page),
			slog.Float64("wait_seconds", wait.Seconds()),
			slog.String("error", err.Error()))
		return nil, fmt.Errorf("%w: %s", ErrTMDBDiscoverUnavailable, err.Error())
	}
	if resp == nil || len(resp.Results) == 0 {
		return nil, nil
	}

	out := make([]disco.Item, 0, len(resp.Results))
	for _, r := range resp.Results {
		it, ok := a.materialiseEntry(ctx, r)
		if !ok {
			continue
		}
		out = append(out, it)
	}
	a.log.InfoContext(ctx, "discovery.discover.fetched",
		slog.Int("page", page),
		slog.Int("results", len(out)),
		slog.Float64("wait_seconds", wait.Seconds()))
	return out, nil
}

// LastWaitSeconds reports the wall-clock spent inside the most recent
// Fetch. Returns 0 before the first call. Read by the handler to size
// the `tmdb_throttled` degraded signal — over 1s flips the flag.
func (a *tmdbPassthroughAdapter) LastWaitSeconds() float64 {
	n := a.lastWaitNanos.Load()
	if n <= 0 {
		return 0
	}
	return float64(n) / float64(time.Second)
}

// materialiseEntry mirrors internal/discovery/app/search.go:144-189 — the
// pattern is duplicated to keep the discovery context free of cross-
// importing the SearchUseCase. Stub-upsert errors are logged at WARN
// and the entry is dropped; a single bad row never fails the whole
// response. (search.go does the same.)
//
// The mapper does NOT enqueue for hot enrichment — Discover is an
// exploration surface, not a watch-list signal. The next scan picks up
// the new stub via the standard enrichment path.
func (a *tmdbPassthroughAdapter) materialiseEntry(ctx context.Context, r tmdb.TVListEntry) (disco.Item, bool) {
	if r.ID <= 0 || r.Name == "" {
		return disco.Item{}, false
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
	sid, err := a.stubs.EnsureStub(ctx, tmdbID, r.Name, poster, backdrop)
	if err != nil {
		a.log.WarnContext(ctx, "discovery.discover.stub_upsert_failed",
			slog.Int64("tmdb_id", int64(tmdbID)),
			slog.String("title", r.Name),
			slog.String("error", err.Error()))
		return disco.Item{}, false
	}
	item := disco.Item{
		SeriesID:     sid,
		TMDBID:       &tmdbID,
		Title:        r.Name,
		PosterPath:   poster,
		BackdropPath: backdrop,
	}
	if y := yearFromFirstAirDate(r.FirstAirDate); y != nil {
		item.Year = y
	}
	if len(r.OriginCountry) > 0 {
		item.OriginCountries = append([]string(nil), r.OriginCountry...)
	}
	return item, true
}
