package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/runtime/quota"
	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	infraomdb "github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// OMDbClientHolder is the late-binding holder satisfying the
// appenrich.OMDbWorker getter contract. Story 213 introduced the holder;
// Story 352 enables runtime swap via the reload subscriber.
//
// Concurrency: Set/Load are race-safe via atomic.Pointer — the worker
// goroutine MAY load the holder concurrently with the reload subscriber
// goroutine swapping it. No mutex needed.
type OMDbClientHolder struct {
	inner atomic.Pointer[infraomdb.Client]
}

// NewOMDbClientHolder constructs an empty holder; Set primes it.
func NewOMDbClientHolder() *OMDbClientHolder { return &OMDbClientHolder{} }

// Get returns the live *omdb.Client coerced into the application port.
// The OMDb worker passes this method as its Client getter.
func (h *OMDbClientHolder) Get() appenrich.OMDbClient {
	if h == nil {
		return nil
	}
	c := h.inner.Load()
	if c == nil {
		return nil
	}
	return c
}

// Load returns the raw client pointer (or nil) for the reload subscriber
// and tests. Distinct from Get because Get returns the application port
// type (which would lose the concrete *omdb.Client identity).
func (h *OMDbClientHolder) Load() *infraomdb.Client {
	if h == nil {
		return nil
	}
	return h.inner.Load()
}

// Set swaps the underlying client. nil clears the holder. Returns the
// previous pointer so the reload subscriber can Close() it after the
// drain window — OMDb has no background goroutine today but the API is
// kept symmetric with TMDBClientHolder.
func (h *OMDbClientHolder) Set(c *infraomdb.Client) *infraomdb.Client {
	return h.inner.Swap(c)
}

// TMDBClientHolder is the runtime-swappable wrapper satisfying the
// appenrich.TMDBClient port. The series + person workers receive the
// holder via constructor injection at boot and never see the underlying
// *tmdb.Client directly, so a reload (key/proxy change) only needs to
// swap the pointer here.
//
// Concurrency: every method goes through atomic.Pointer.Load. Set Swap
// returns the previous client so the reload subscriber can drain + close
// the old rate-limiter goroutine after a grace window.
//
// Nil-load semantics: when the holder is empty (TMDB disabled at runtime
// after an operator flip) every method returns ErrTMDBClientNotReady so
// the worker records an enrichment_errors row with a retry-due. The
// dispatcher's loop logs the error and continues serving.
type TMDBClientHolder struct {
	inner atomic.Pointer[tmdb.Client]
}

// NewTMDBClientHolder constructs an empty holder; Set primes it.
func NewTMDBClientHolder() *TMDBClientHolder { return &TMDBClientHolder{} }

// Set swaps the underlying client. Returns the previous client so the
// caller can Close() it after in-flight requests drain.
func (h *TMDBClientHolder) Set(c *tmdb.Client) *tmdb.Client {
	return h.inner.Swap(c)
}

// Load returns the live client or nil.
func (h *TMDBClientHolder) Load() *tmdb.Client {
	if h == nil {
		return nil
	}
	return h.inner.Load()
}

// ErrTMDBClientNotReady is returned by holder methods when the operator
// has disabled TMDB at runtime (the boot path early-returns and leaves
// the holder unallocated for boot-disabled TMDB, so this error fires
// only on a runtime disable).
var ErrTMDBClientNotReady = fmt.Errorf("tmdb: client not configured")

func (h *TMDBClientHolder) GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error) {
	c := h.Load()
	if c == nil {
		return nil, ErrTMDBClientNotReady
	}
	return c.GetTV(ctx, id, language)
}

func (h *TMDBClientHolder) GetSeason(ctx context.Context, tvID int64, seasonNumber int, language string) (*tmdb.SeasonResponse, error) {
	c := h.Load()
	if c == nil {
		return nil, ErrTMDBClientNotReady
	}
	return c.GetSeason(ctx, tvID, seasonNumber, language)
}

func (h *TMDBClientHolder) GetPerson(ctx context.Context, id int64, language string) (*tmdb.PersonResponse, error) {
	c := h.Load()
	if c == nil {
		return nil, ErrTMDBClientNotReady
	}
	return c.GetPerson(ctx, id, language)
}

func (h *TMDBClientHolder) FindByTVDB(ctx context.Context, tvdbID domain.TVDBID) (*tmdb.FindResponse, error) {
	c := h.Load()
	if c == nil {
		return nil, ErrTMDBClientNotReady
	}
	return c.FindByTVDB(ctx, tvdbID)
}

// Trending forwards to the live tmdb.Client, returning the wrapped
// not-ready error when the operator has TMDB disabled at runtime.
// Added for the DiscoveryWorker (story 506) — same Load+nil-check
// pattern as GetTV / GetSeason / GetPerson / FindByTVDB.
func (h *TMDBClientHolder) Trending(ctx context.Context, scope tmdb.TrendingScope, language string, page int) (*tmdb.TVListResponse, error) {
	c := h.Load()
	if c == nil {
		return nil, ErrTMDBClientNotReady
	}
	return c.Trending(ctx, scope, language, page)
}

// Popular forwards to the live tmdb.Client; DiscoveryWorker entry
// point for the popular leaderboard.
func (h *TMDBClientHolder) Popular(ctx context.Context, language string, page int) (*tmdb.TVListResponse, error) {
	c := h.Load()
	if c == nil {
		return nil, ErrTMDBClientNotReady
	}
	return c.Popular(ctx, language, page)
}

// DiscoverTV forwards to the live tmdb.Client; DiscoveryWorker entry
// point for the by_genre / by_network / by_keyword curated lists.
func (h *TMDBClientHolder) DiscoverTV(ctx context.Context, filter tmdb.DiscoverFilter, page int) (*tmdb.TVListResponse, error) {
	c := h.Load()
	if c == nil {
		return nil, ErrTMDBClientNotReady
	}
	return c.DiscoverTV(ctx, filter, page)
}

// Compile-time guarantee: *TMDBClientHolder satisfies the application
// port. Caught at build time if the port ever grows a new method.
var _ appenrich.TMDBClient = (*TMDBClientHolder)(nil)

// BuildOMDbClient is the factory the boot wiring + the reload subscriber
// share. Pinned in this package so both paths use identical defaults
// (per-call timeout, BaseURL, metrics-transport wrap).
func BuildOMDbClient(settings infraextsvc.Settings) (*infraomdb.Client, error) {
	httpClient, err := infraextsvc.HttpClientFor(settings)
	if err != nil {
		return nil, fmt.Errorf("omdb http client: %w", err)
	}
	c, err := infraomdb.New(infraomdb.Config{
		APIKey:     settings.APIKey,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("omdb client: %w", err)
	}
	return c, nil
}

// TMDBClientFactoryConfig pins the immutable construction parameters
// the reload subscriber MUST carry into BuildTMDBClient — proxy / key
// come from the live Settings but RPS + Language + Logger are wired at
// boot and never change at runtime.
//
// Story 489 (B-17) — AuthFailureReporter is the optional 401-hook
// passed to tmdb.New so every reload-rebuilt TMDB client surfaces
// auth failures into the externalservices.UseCase validation cache.
// Nil-OK; tests skip wiring it.
type TMDBClientFactoryConfig struct {
	Language            string
	RPS                 float64
	Logger              *slog.Logger
	AuthFailureReporter tmdb.AuthFailureReporter
	// QuotaCounter is the optional B-1 observability sink. Nil-OK —
	// when nil, the rebuilt client neither Increments nor publishes
	// the seasonfill_external_service_quota_used{service="tmdb"} gauge.
	// Threaded through every reload subscriber rebuild so a runtime
	// key swap preserves the observability wiring.
	QuotaCounter quota.QuotaCounter
}

// BuildTMDBClient is the factory the boot wiring + the reload subscriber
// share. Mirrors the boot path in wiring.BuildEnrichment so the API
// rate limiter + Bearer auth + metrics-transport wrap stay byte-
// identical to what the worker saw at boot.
//
// IMPORTANT — caveat: the returned client's http.Client is freshly
// minted (own connection pool, own metrics transport label "tmdb").
// The image.tmdb.org downloader's separately minted http.Client is NOT
// swapped here — Story 352 reloads the API key + proxy for
// api.themoviedb.org only.
func BuildTMDBClient(settings infraextsvc.Settings, cfg TMDBClientFactoryConfig) (*tmdb.Client, error) {
	httpClient, err := infraextsvc.HttpClientFor(settings)
	if err != nil {
		return nil, fmt.Errorf("tmdb http client: %w", err)
	}
	c, err := tmdb.New(tmdb.Config{
		Token:               settings.APIKey,
		HTTPClient:          httpClient,
		Language:            cfg.Language,
		RPS:                 cfg.RPS,
		Logger:              cfg.Logger,
		AuthFailureReporter: cfg.AuthFailureReporter, // Story 489 (B-17)
		QuotaCounter:        cfg.QuotaCounter,        // B-1
	})
	if err != nil {
		return nil, fmt.Errorf("tmdb client: %w", err)
	}
	return c, nil
}

// proxySchemeFor extracts a slog-friendly scheme label from a proxy
// URL. Empty proxy → "none". Used by the client subscribers + the
// existing extsvc log lines so operators can grep one consistent
// schema across both surfaces.
func proxySchemeFor(raw string) string {
	if raw == "" {
		return "none"
	}
	for i := 0; i < len(raw)-2; i++ {
		if raw[i] == ':' && raw[i+1] == '/' && raw[i+2] == '/' {
			scheme := raw[:i]
			// Lowercase manually to avoid a strings import in a tiny
			// helper — schemes are ASCII.
			out := make([]byte, len(scheme))
			for j, b := range []byte(scheme) {
				if b >= 'A' && b <= 'Z' {
					b += 'a' - 'A'
				}
				out[j] = b
			}
			return string(out)
		}
	}
	return "unknown"
}
