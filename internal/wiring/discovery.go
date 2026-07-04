package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	discopersistence "github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// discovery.go wires the discovery bounded-context persistence
// + cross-context adapter set (PRD §5.1.1 / story 505 N-2d).
//
// The vertical-slice rule (PRD §3.3) forbids internal/discovery/ from
// importing internal/enrichment/ directly. The StubUpserter adapter
// lives here in the wiring package precisely to bridge that boundary:
// wiring is allowed to import every context, so it can compose a
// discoapp.StubUpserter from
// enrichpersistence.SeriesRepository.UpsertStub.
//
// Future stories (506 worker, 507 handlers) call BuildDiscoveryPersistence
// from server.go and read through the returned bundle.

// DiscoveryPersistenceBundle groups the discovery persistence + adapters
// constructed at boot. Story 505 ships ListRepo + LangProvider + Stubs;
// story 506 adds the worker and consumes all three.
type DiscoveryPersistenceBundle struct {
	ListRepo     *discopersistence.ListRepository
	LangProvider *discopersistence.ActiveLanguagesRepository
	Stubs        discoapp.StubUpserter
}

// BuildDiscoveryPersistence wires the three discovery persistence
// components.
//
// The signature accepts the enrichment SeriesRepository (NOT the
// kernel DB handle) so the adapter wraps an existing repo pointer
// rather than re-constructing one. Server.go calls this AFTER it has
// built the enrichment bundle.
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers (room for
// future seed-or-validate logic).
func BuildDiscoveryPersistence(
	persistence *PersistenceBundle,
	seriesRepo *enrichpersistence.SeriesRepository,
) (*DiscoveryPersistenceBundle, error) {
	db := persistence.DB
	return &DiscoveryPersistenceBundle{
		ListRepo:     discopersistence.NewListRepository(db),
		LangProvider: discopersistence.NewActiveLanguagesRepository(db),
		Stubs: &stubUpserterAdapter{
			seriesRepo:       seriesRepo,
			seriesTexts:      enrichpersistence.NewSeriesTextsRepository(db),
			seriesMediaTexts: enrichpersistence.NewSeriesMediaTextsRepository(db),
		},
	}, nil
}

// DiscoveryRuntimeBundle groups the worker + supporting reader
// constructed at boot. Server.go consumes Worker via the
// cmd/server/loops/discovery.go entry point.
type DiscoveryRuntimeBundle struct {
	Worker   *discoapp.Worker
	TopKinds *discopersistence.TopKindsReader
}

// realDiscoveryClock satisfies discoapp.Clock with time.Now().
type realDiscoveryClock struct{}

func (realDiscoveryClock) Now() time.Time { return time.Now() }

// DiscoveryRuntimeDeps is the input contract for BuildDiscoveryRuntime.
// All fields required — nil causes a wiring error before NewWorker is
// reached — EXCEPT PreWarmer (Story 568 A2) which is nil-OK. When
// PreWarmer is nil the worker's refresh() success branch skips the
// A2 pre-warm fan-out (config toggle OFF or unwired boot race).
type DiscoveryRuntimeDeps struct {
	Persistence *DiscoveryPersistenceBundle
	DB          *gorm.DB
	TMDB        discoapp.TMDBClient
	Log         *slog.Logger
	// PreWarmer — Story 568 A2. Optional; nil disables the pre-warm
	// fan-out. Production wiring passes an
	// *adapters.DiscoveryPreWarmerHolder that binds
	// enrichment.SeriesWorker.RefreshSeriesText after the LATE BIND
	// ZONE in cmd/server/server.go.
	PreWarmer discoapp.SeriesTextPreWarmer
}

// BuildDiscoveryRuntime wires the worker + top-kinds reader. The
// caller is server.go, which:
//  1. invokes BuildDiscoveryPersistence first to get the repos +
//     stub adapter,
//  2. passes the live TMDBClientHolder (cmd/server/adapters) as the
//     discoapp.TMDBClient,
//  3. starts the loop via cmd/server/loops.RunDiscovery on
//     lifecycle.Go.
//
// The Log argument MUST already carry the "discovery" domain tag —
// callers should pass sharedports.DomainLogger(log, "discovery").
// The construction is in-memory only; an error path is returned for
// symmetry with the sibling Build* wirers (room for future
// boot-time validation).
func BuildDiscoveryRuntime(deps DiscoveryRuntimeDeps) (*DiscoveryRuntimeBundle, error) {
	if deps.Persistence == nil {
		return nil, fmt.Errorf("discovery runtime: persistence required")
	}
	if deps.DB == nil {
		return nil, fmt.Errorf("discovery runtime: db required")
	}
	if deps.TMDB == nil {
		return nil, fmt.Errorf("discovery runtime: tmdb client required")
	}
	if deps.Log == nil {
		return nil, fmt.Errorf("discovery runtime: log required")
	}
	topKinds := discopersistence.NewTopKindsReader(deps.DB)
	// B-39: production-tuned limiter paces refresh() so cold-start
	// fan-out doesn't overrun the enrichment prewarm queue. Constants
	// live in discoapp so tests can reference the same values; here we
	// hand the worker its very own *rate.Limiter so concurrent on-demand
	// RefreshNow calls share the same budget as the Tick loop.
	worker := discoapp.NewWorker(discoapp.WorkerDeps{
		Repo:     deps.Persistence.ListRepo,
		Langs:    deps.Persistence.LangProvider,
		Stubs:    deps.Persistence.Stubs,
		TMDB:     deps.TMDB,
		TopKinds: topKinds,
		Log:      deps.Log,
		Clock:    realDiscoveryClock{},
		Limiter: rate.NewLimiter(
			rate.Limit(discoapp.DefaultRefreshRPS),
			discoapp.DefaultRefreshBurst,
		),
		// Story 568 A2 — PreWarmer shares the Worker.limiter internally
		// (no dual TMDB rate budget). Nil-safe: when the operator sets
		// discoveryPreWarm.enabled=false the caller passes nil here and
		// the worker's refresh() success branch skips the pre-warm
		// fan-out.
		PreWarmer: deps.PreWarmer,
	})
	return &DiscoveryRuntimeBundle{
		Worker:   worker,
		TopKinds: topKinds,
	}, nil
}

// stubUpserterAdapter bridges enrichpersistence.SeriesRepository.UpsertStub
// (which takes the rich series.Canon) to the narrow discoapp.StubUpserter
// port (which takes only the tmdb_id + title + poster + backdrop a
// discovery worker has on hand from a TMDB Trending/Popular response).
//
// The adapter materialises a stub Canon with Hydration="stub" so the
// COALESCE-protected UpsertStub path correctly merges against any pre-
// existing row without downgrading a 'full' hydration to 'stub'
// (see SeriesRepository.UpsertStub godoc for the merge invariants).
type stubUpserterAdapter struct {
	seriesRepo       *enrichpersistence.SeriesRepository
	seriesTexts      *enrichpersistence.SeriesTextsRepository
	seriesMediaTexts *enrichpersistence.SeriesMediaTextsRepository
}

func (a *stubUpserterAdapter) EnsureStub(
	ctx context.Context,
	tmdbID shareddomain.TMDBID,
	lang, title, originalTitle, originalLanguage string,
	poster, backdrop *string,
) (shareddomain.SeriesID, error) {
	if title == "" {
		return 0, fmt.Errorf("discovery stub upserter: title required")
	}
	if lang == "" {
		lang = tmdb.DefaultLanguage
	}
	// Copy tmdbID into a local before taking its address so the pointer
	// in series.Canon does not alias the caller's parameter slot — the
	// adapter must own the lifetime of the value it writes through.
	tmdbCopy := tmdbID
	// Seed original_title/original_language on the canon stub so the
	// W15-2 never-empty original_title fallback tier is alive for
	// discovery-materialised stubs. UpsertStub COALESCEs existing-first
	// so a re-EnsureStub never clobbers an enriched value.
	canon := series.Canon{
		TMDBID:           &tmdbCopy,
		Hydration:        series.HydrationStub,
		OriginalTitle:    nonEmptyPtr(originalTitle),
		OriginalLanguage: nonEmptyPtr(originalLanguage),
	}
	seriesID, err := a.seriesRepo.UpsertStub(ctx, canon)
	if err != nil {
		return 0, err
	}
	// Seed series_texts{lang} (the CALL language, never poisoning en-US
	// with a foreign-language name) only-if-absent so an enriched row is
	// never clobbered. Readers in OTHER languages fall back through the
	// original_title tier seeded above.
	if a.seriesTexts != nil {
		t := title
		if terr := a.seriesTexts.InsertBaseLangIfAbsent(ctx, series.SeriesText{
			SeriesID: seriesID,
			Language: lang,
			Title:    &t,
		}); terr != nil {
			return 0, fmt.Errorf("discovery stub seed series_texts: %w", terr)
		}
	}
	// Seed series_media_texts{lang} from the payload poster/backdrop the
	// TMDB list response already carried (raw TMDB paths, the same format
	// RefreshAllLangs stores in poster_asset/backdrop_asset). Only-if-
	// absent: the per-lang RefreshAllLangs writer stays authoritative and
	// overwrites this seed (with a resolved hash) later.
	if a.seriesMediaTexts != nil && (poster != nil || backdrop != nil) {
		if merr := a.seriesMediaTexts.InsertIfAbsent(ctx, series.SeriesMediaText{
			SeriesID:      seriesID,
			Language:      lang,
			PosterAsset:   poster,
			BackdropAsset: backdrop,
		}); merr != nil {
			return 0, fmt.Errorf("discovery stub seed series_media_texts: %w", merr)
		}
	}
	return seriesID, nil
}

// nonEmptyPtr returns a pointer to s, or nil when s == "". Used so an
// empty original_title/original_language from a TMDB list row seeds a
// SQL NULL (COALESCE-preservable) rather than an empty string.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// libraryInstancesAdapter bridges catalog SeriesCacheRepository to the
// narrow discoapp.LibraryInstancesPort. Same precedent as
// stubUpserterAdapter — wiring is the only package allowed to bridge
// across the discovery → catalog boundary (PRD §3.3 vertical slice).
//
// The adapter unwraps the typed []domain.InstanceName slice to a
// []string per the discoapp port contract, so handlers don't carry a
// catalog domain import.
type libraryInstancesAdapter struct {
	cache catalogLibraryInstancesReader
}

// catalogLibraryInstancesReader is the minimal SeriesCacheRepository
// surface the adapter reads through. Declared inline so the adapter
// stays testable with a hand-rolled stub without spinning a Postgres
// container — every wiring-level unit test that exercises the
// constructor uses this seam.
type catalogLibraryInstancesReader interface {
	GetInstancesBySeriesIDs(ctx context.Context, seriesIDs []shareddomain.SeriesID) (map[shareddomain.SeriesID][]shareddomain.InstanceName, error)
}

func (a *libraryInstancesAdapter) ListByCanonicalSeriesIDs(
	ctx context.Context,
	ids []shareddomain.SeriesID,
) (map[shareddomain.SeriesID][]string, error) {
	if len(ids) == 0 {
		return map[shareddomain.SeriesID][]string{}, nil
	}
	raw, err := a.cache.GetInstancesBySeriesIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("library instances adapter: %w", err)
	}
	out := make(map[shareddomain.SeriesID][]string, len(raw))
	for sid, instances := range raw {
		strs := make([]string, 0, len(instances))
		for _, in := range instances {
			strs = append(strs, string(in))
		}
		out[sid] = strs
	}
	return out, nil
}

// DiscoveryHTTPBundle groups the HTTP-layer wiring for story 507 +
// 508 (SearchUC) + story 509 (DiscoverHandler) + story 520 (N-4c
// AddToSonarr).
type DiscoveryHTTPBundle struct {
	Handler            *discoveryrest.DiscoveryHandler
	DiscoverHandler    *discoveryrest.DiscoverHandler    // story 509 N-2h
	AddToSonarrHandler *discoveryrest.AddToSonarrHandler // story 520 N-4c
	Genres             *discopersistence.GenresPickerRepo
	Networks           *discopersistence.NetworksPickerRepo
	SearchUC           *discoapp.SearchUseCase // story 508 (N-2g); nil when TMDB disabled
}

// BuildDiscoveryHTTP wires the story 507 N-2f HTTP handler + the
// story 508 N-2g search use case.
//
// The Worker arg satisfies BOTH narrow ports the handler reads — the
// concrete *Worker is passed; Go's interface satisfaction unifies
// (worker.IsWarming, worker.RefreshNow) onto the
// (WarmingProbe, RefreshOnDemand) tuple at the call site.
//
// tmdb / stubs / dispatcher are the SearchUseCase dependencies (story
// 508). Any nil → searchUC is nil and the handler returns 503
// search_unavailable on /discovery/search.
//
// resolver — story 526. Optional shared *media.Resolver used by
// projectItem to translate raw TMDB image paths into sha256 wire
// hashes. Nil-OK (legacy raw-path behavior). Wiring threads the same
// instance the seriesdetail composers hold so cache slots are shared.
//
// libraryInstances — story 527 (Bug 2 fix). Optional catalog
// SeriesCacheRepository surface used by projectItem to populate
// DiscoverySeriesItem.InLibraryInstances. Nil-OK: when nil, the slice
// ships as []string{} for every item (legacy pre-527 behavior — UX
// regresses to "+ Add to Sonarr always visible", never panics).
//
// log MUST already carry the "discovery" domain tag.
func BuildDiscoveryHTTP(
	persistence *PersistenceBundle,
	runtime *DiscoveryRuntimeBundle,
	listRepo discoapp.DiscoveryListRepo,
	tmdb discoapp.SearchTMDB,
	stubs discoapp.StubUpserter,
	dispatcher discoapp.EnrichmentDispatcher,
	resolver *media.Resolver,
	libraryInstances catalogLibraryInstancesReader,
	log *slog.Logger,
) *DiscoveryHTTPBundle {
	genres := discopersistence.NewGenresPickerRepo(persistence.DB)
	networks := discopersistence.NewNetworksPickerRepo(persistence.DB)

	var searchUC *discoapp.SearchUseCase
	if tmdb != nil && stubs != nil && dispatcher != nil {
		searchRepo := discopersistence.NewSearchRepository(persistence.DB)
		searchUC = discoapp.NewSearchUseCase(searchRepo, tmdb, stubs, dispatcher, log)
	}

	var libPort discoapp.LibraryInstancesPort
	if libraryInstances != nil {
		libPort = &libraryInstancesAdapter{cache: libraryInstances}
	}

	h := discoveryrest.NewDiscoveryHandler(
		listRepo,
		runtime.Worker, // satisfies WarmingProbe
		runtime.Worker, // satisfies RefreshOnDemand
		genres,
		networks,
		searchUC, // nil-OK; handler returns 503 search_unavailable
		resolver, // nil-OK; raw TMDB paths flow through unchanged
		libPort,  // nil-OK; in_library_instances stays []string{}
		log,
	)
	return &DiscoveryHTTPBundle{
		Handler:  h,
		Genres:   genres,
		Networks: networks,
		SearchUC: searchUC,
	}
}

// EnrichmentDispatcherAdapter bridges the discoapp.EnrichmentDispatcher
// port (kind/priority as strings) to the concrete appenrich.Dispatcher
// (typed kind + Priority constants). Lives in wiring so discovery never
// imports enrichment.
//
// Exported so server.go can construct it without an extra wiring
// constructor.
type EnrichmentDispatcherAdapter struct {
	Inner appenrich.Dispatcher
}

// Enqueue translates the discovery string enums into the appenrich
// typed constants. Unknown entity kinds drop silently — the cold-start
// search path is series-only by design.
func (a *EnrichmentDispatcherAdapter) Enqueue(entity string, id int64, priority string) {
	if a.Inner == nil {
		return
	}
	var kind appenrich.EntityKind
	switch entity {
	case discoapp.EntitySeriesKind:
		kind = appenrich.EntitySeries
	default:
		return
	}
	var p appenrich.Priority
	switch priority {
	case discoapp.PriorityHotLabel:
		p = appenrich.PriorityHot
	default:
		p = appenrich.PriorityCold
	}
	a.Inner.Enqueue(kind, id, p)
}

// DiscoveryDiscoverBundle groups the cache + bg fetcher + handler wired
// for /discovery/discover (story 509 N-2h).
type DiscoveryDiscoverBundle struct {
	Handler   *discoveryrest.DiscoverHandler
	BgFetcher *discoapp.BgFetcher
	LRU       *cachewatch.Cache[string, []disco.Item]
}

// DiscoveryDiscoverDeps is the input contract for BuildDiscoveryDiscover.
type DiscoveryDiscoverDeps struct {
	TMDBClient discoapp.TMDBDiscoverClient
	Stubs      discoapp.StubUpserter
	Worker     discoapp.WarmingProbe
	// Resolver — story 526. Optional shared *media.Resolver threaded
	// into the DiscoverHandler so /discovery/discover rewrites raw
	// TMDB paths to sha256 wire hashes (same projection contract as
	// the curated endpoints). Nil-OK.
	Resolver *media.Resolver
	// LibraryInstances — story 527. Optional catalog surface used by
	// DiscoverHandler's projectSearchItems loop to populate
	// in_library_instances. Nil-OK (legacy pre-527 behavior).
	LibraryInstances catalogLibraryInstancesReader
	Log              *slog.Logger
}

// BuildDiscoveryDiscover wires the LRU + passthrough + bg fetcher +
// handler. Caller (cmd/server/server.go) launches BgFetcher.RunWorker
// on lifecycle.Go.
//
// LRU sizing per PRD §5.1.2 line 811: 1000 entries, TTL 1h. Sizer
// estimates ~500 bytes per disco.Item.
func BuildDiscoveryDiscover(deps DiscoveryDiscoverDeps) *DiscoveryDiscoverBundle {
	switch {
	case deps.TMDBClient == nil:
		panic("BuildDiscoveryDiscover: TMDBClient required")
	case deps.Stubs == nil:
		panic("BuildDiscoveryDiscover: Stubs required")
	case deps.Worker == nil:
		panic("BuildDiscoveryDiscover: Worker required")
	case deps.Log == nil:
		panic("BuildDiscoveryDiscover: Log required")
	}
	sizer := func(k string, v []disco.Item) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.Item]("discover", 1000, 1*time.Hour, sizer)
	pass := discoapp.NewTMDBPassthrough(deps.TMDBClient, deps.Stubs, deps.Log)
	bg := discoapp.NewBgFetcher(lru, pass, deps.Log)

	var libPort discoapp.LibraryInstancesPort
	if deps.LibraryInstances != nil {
		libPort = &libraryInstancesAdapter{cache: deps.LibraryInstances}
	}

	handler := discoveryrest.NewDiscoverHandler(lru, pass, bg, deps.Worker, deps.Resolver, libPort, deps.Log)
	return &DiscoveryDiscoverBundle{
		Handler:   handler,
		BgFetcher: bg,
		LRU:       lru,
	}
}

// === Story 540 / B-49 — Discovery genre catalog sync ===

// DiscoveryGenreSyncBundle groups the syncer constructed at boot.
type DiscoveryGenreSyncBundle struct {
	Syncer *discoapp.GenreSyncer
}

// TMDBGenreClient is the slice of *tmdb.Client the syncer needs. The
// production binder is *adapters.TMDBClientHolder once it gains a
// GenreListTV pass-through; the holder method is added in this story.
type TMDBGenreClient interface {
	GenreListTV(ctx context.Context, language string) (*tmdb.GenreListResponse, error)
}

// genreListerAdapter bridges TMDBGenreClient → discoapp.TMDBGenreLister.
// The package boundary forbids internal/discovery/ from importing the
// concrete tmdb.GenreListResponse; the adapter performs the field
// rename (Genres → Items).
type genreListerAdapter struct {
	inner TMDBGenreClient
}

func (a *genreListerAdapter) GenreListTV(ctx context.Context, language string) (*discoapp.GenreListResult, error) {
	resp, err := a.inner.GenreListTV(ctx, language)
	if err != nil {
		return nil, err
	}
	out := make([]discoapp.GenreListItem, 0, len(resp.Genres))
	for _, g := range resp.Genres {
		out = append(out, discoapp.GenreListItem{ID: g.ID, Name: g.Name})
	}
	return &discoapp.GenreListResult{Items: out}, nil
}

// DiscoveryGenreSyncDeps is the input contract.
type DiscoveryGenreSyncDeps struct {
	Genres    *enrichpersistence.GenresRepository
	I18n      *enrichpersistence.GenresI18nRepository
	TMDB      TMDBGenreClient
	Languages []string
	Log       *slog.Logger
}

// BuildDiscoveryGenreSync wires the syncer over the enrichment repos +
// the live TMDB client. Languages defaults to locale.SupportedUserLanguages
// when nil — caller passes a copied slice so future runtime-reload
// mutations don't race.
func BuildDiscoveryGenreSync(deps DiscoveryGenreSyncDeps) (*DiscoveryGenreSyncBundle, error) {
	if deps.Genres == nil {
		return nil, fmt.Errorf("discovery genre sync: genres repo required")
	}
	if deps.I18n == nil {
		return nil, fmt.Errorf("discovery genre sync: i18n repo required")
	}
	if deps.TMDB == nil {
		return nil, fmt.Errorf("discovery genre sync: tmdb client required")
	}
	if deps.Log == nil {
		return nil, fmt.Errorf("discovery genre sync: log required")
	}
	if len(deps.Languages) == 0 {
		deps.Languages = append([]string(nil), locale.SupportedUserLanguages...)
	}
	syncer := discoapp.NewGenreSyncer(discoapp.GenreSyncerDeps{
		TMDB:      &genreListerAdapter{inner: deps.TMDB},
		Genres:    deps.Genres,
		I18n:      deps.I18n,
		Languages: deps.Languages,
		Log:       deps.Log,
	})
	return &DiscoveryGenreSyncBundle{Syncer: syncer}, nil
}
