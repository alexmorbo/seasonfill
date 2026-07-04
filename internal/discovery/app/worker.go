// worker.go runs the discovery refresh loop (PRD §5.1.1 lines
// 623-710 + cold-start UX line 666). Stateless wrt scheduling — every
// Tick queries DiscoveryListRepo.IsStale to decide whether to fetch.
// In-process schedule cache would diverge from the DB on a multi-pod
// rollout; the repo is the single source of truth.
//
// Cold-start contract: RunForever fires Tick(ctx) IMMEDIATELY on
// entry, then schedules the next Tick on a time.NewTicker(interval).
// Without this the first lists appear an hour after pod ready —
// PRD §5.1.1 line 666 forbids that wait.
//
// Import rule (PRD §3.3): app imports internal/discovery/domain +
// internal/shared/* + stdlib only. The narrow TMDBClient + Clock
// interfaces are defined HERE so unit tests can pass fakes; the
// production *tmdb.Client (story 504) satisfies the surface via
// duck typing and is bound by the wiring layer.
package app

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MaxActiveLanguages caps the active-language fan-out at one Tick.
// PRD §5.1.1 leaves the upper bound implicit; the homelab scale
// never exceeds 10 distinct preferred_language rows. >10 is logged
// as a warn and truncated so a misconfigured users table can't
// stall the worker on a TMDB rate-limit storm.
const MaxActiveLanguages = 10

// TopKindsLimit is the per-tick fan-out for by_genre / by_network
// refreshes — top-10 per kind per language per PRD §5.1.1 line 645.
const TopKindsLimit = 10

// DefaultRefreshRPS is the steady-state ceiling on refresh starts per
// second (B-39). Tuned so the cold-start sweep of ~46 (lang × kind) pairs
// spreads its stub-upsert + enrichment-enqueue fan-out without overrunning
// the media prewarm queue's 30s window.
const DefaultRefreshRPS = 5

// DefaultRefreshBurst is the immediate-burst budget — covers the
// 3 leaderboards × N langs hot-path on cold start before the rps cap kicks
// in. PRD §5.1.1 leaves the figure implicit; 20 was chosen so the first
// curated genre/network refreshes (the user-visible "fresh discovery") land
// inside ~4s post-boot.
const DefaultRefreshBurst = 20

// TMDBClient is the narrow surface the worker reads through. The
// production *tmdb.Client (story 504) satisfies this by signature
// match; tests pass an in-memory fake.
//
// Trending takes a TrendingScope (day|week); Popular takes only
// language+page; DiscoverTV reads filter+page+the client's default
// language (filter struct does NOT carry a language field per the
// DiscoverFilter contract — by_genre/by_network ride the client
// default language, which the wiring layer sets per-call by
// constructing a per-language tmdb.Client adapter — see story 506
// wiring notes).
type TMDBClient interface {
	Trending(ctx context.Context, scope tmdb.TrendingScope, language string, page int) (*tmdb.TVListResponse, error)
	Popular(ctx context.Context, language string, page int) (*tmdb.TVListResponse, error)
	DiscoverTV(ctx context.Context, filter tmdb.DiscoverFilter, page int) (*tmdb.TVListResponse, error)
}

// TopKindsProvider returns the top-N TMDB genre / network ids by
// local catalog occurrence. Implementation:
// internal/discovery/persistence.TopKindsReader.
type TopKindsProvider interface {
	TopGenres(ctx context.Context, limit int) ([]int, error)
	TopNetworks(ctx context.Context, limit int) ([]int, error)
}

// Clock is the narrow time port the worker reads through so tests
// can pin Now() deterministically. Production: realClock{} wraps
// time.Now (provided in wiring/discovery.go).
type Clock interface {
	Now() time.Time
}

// WorkerDeps groups the worker dependencies for constructor-arg
// hygiene. Every field is required — NewWorker panics on nil — EXCEPT
// Limiter, which defaults to a production-tuned rate.Limiter when nil,
// and PreWarmer, which is nil-OK (A2 disabled → refresh() success
// branch skips the pre-warm fan-out).
type WorkerDeps struct {
	Repo     DiscoveryListRepo
	Langs    ActiveLanguagesProvider
	Stubs    StubUpserter
	TMDB     TMDBClient
	TopKinds TopKindsProvider
	Log      *slog.Logger
	Clock    Clock
	// Limiter paces refresh() calls (B-39). Optional — nil falls back
	// to rate.NewLimiter(DefaultRefreshRPS, DefaultRefreshBurst). Tests
	// pass a faster limiter (e.g. rate.NewLimiter(rate.Inf, 1)) to keep
	// runtime short, or a slower one to assert the throttle fires.
	Limiter *rate.Limiter
	// PreWarmer — Story 568 A2. Nil-OK; when nil the worker's
	// refresh() success branch does NOT fan out per-lang RefreshSeriesText
	// calls. Production wiring binds the SeriesTextPreWarmer adapter over
	// the enrichment SeriesWorker.RefreshSeriesText when
	// discoveryPreWarm.enabled=true (default true). PreWarmer shares
	// the same Limiter — no dual TMDB rate budget.
	PreWarmer SeriesTextPreWarmer
}

// Worker is the 1h refresh loop owner. Single-threaded — Tick is
// driven by RunForever on one goroutine, so no per-(kind,lang) lock
// is needed inside Tick. Concurrency at the DB level is the
// repo.ReplaceList row-lock contract (story 505 list_repository
// godoc).
type Worker struct {
	repo     DiscoveryListRepo
	langs    ActiveLanguagesProvider
	stubs    StubUpserter
	tmdb     TMDBClient
	topKinds TopKindsProvider
	log      *slog.Logger
	clock    Clock

	// limiter paces refresh() calls — both Tick-driven and on-demand
	// (RefreshNow) paths. See WorkerDeps.Limiter godoc for tuning notes
	// and B-39 for the production failure mode this guards against.
	limiter *rate.Limiter

	// preWarmer — Story 568 A2. Nil-OK; when nil the refresh() success
	// branch skips the per-lang RefreshSeriesText fan-out. Shares the
	// same limiter as refresh() itself (§3.4 story spec).
	preWarmer SeriesTextPreWarmer

	// warmingOnce flips discovery_warming 1→0 exactly once, on the
	// first successful ReplaceList of ANY kind. atomic.Bool +
	// CompareAndSwap so two simultaneous Tick branches can't double-
	// flip (Tick is single-threaded today; the atomic is cheap
	// insurance for a future fan-out).
	warmingOnce atomic.Bool
}

// NewWorker constructs the worker. Panics on nil dependencies — the
// boot path constructs the worker through wiring.BuildDiscoveryRuntime
// which always provides every dependency, so a panic here surfaces a
// wiring bug at first boot rather than at first Tick.
func NewWorker(deps WorkerDeps) *Worker {
	switch {
	case deps.Repo == nil:
		panic("discovery worker: Repo required")
	case deps.Langs == nil:
		panic("discovery worker: Langs required")
	case deps.Stubs == nil:
		panic("discovery worker: Stubs required")
	case deps.TMDB == nil:
		panic("discovery worker: TMDB required")
	case deps.TopKinds == nil:
		panic("discovery worker: TopKinds required")
	case deps.Log == nil:
		panic("discovery worker: Log required")
	case deps.Clock == nil:
		panic("discovery worker: Clock required")
	}
	// Warming gauge starts at 1 — the first successful ReplaceList
	// flips it to 0. Set unconditionally on construction so a pod
	// that crashes mid-warmup re-publishes 1 after restart.
	observability.SetDiscoveryWarming(true)
	limiter := deps.Limiter
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Limit(DefaultRefreshRPS), DefaultRefreshBurst)
	}
	return &Worker{
		repo:      deps.Repo,
		langs:     deps.Langs,
		stubs:     deps.Stubs,
		tmdb:      deps.TMDB,
		topKinds:  deps.TopKinds,
		log:       deps.Log,
		clock:     deps.Clock,
		limiter:   limiter,
		preWarmer: deps.PreWarmer,
	}
}

// RunForever blocks until ctx is cancelled. First tick fires
// immediately (PRD §5.1.1 cold-start contract); thereafter on a
// 1h cadence (the production interval — RunForever itself is
// interval-agnostic so the loop entry point in cmd/server/loops
// owns the policy).
func (w *Worker) RunForever(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	// Cold-start: first tick blocking on entry. Errors are surfaced
	// via per-refresh warn logs inside Tick — RunForever does NOT
	// propagate Tick errors (cron-resilient).
	_ = w.Tick(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = w.Tick(ctx)
		}
	}
}

// Tick is one pass over the (lang × kind) matrix. Per (lang, kind),
// the worker:
//  1. Queries repo.IsStale(kind, "", lang, ScheduleFor(kind)).
//  2. If stale, fetches PagesFor(kind) pages from TMDB and atomically
//     replaces the list via repo.ReplaceList.
//  3. If PreWarmer wired, fans out RefreshSeriesText(force=false) per
//     (item, activeLang) pair via preWarmSeriesTexts (Story 568 A2).
//
// Errors at any step short-circuit the (lang, kind) pair only — the
// next pair continues. Tick itself returns only ctx.Err() or the
// active-languages lookup error; per-(kind,lang) failures are swallowed
// (cron-resilient — RunForever ignores the return value).
func (w *Worker) Tick(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	langs, err := w.langs.ActiveLanguages(ctx)
	if err != nil {
		w.log.WarnContext(ctx, "discovery active languages query failed",
			slog.String("error", err.Error()))
		return err
	}
	if len(langs) > MaxActiveLanguages {
		w.log.WarnContext(ctx, "discovery active languages truncated",
			slog.Int("got", len(langs)),
			slog.Int("cap", MaxActiveLanguages))
		langs = langs[:MaxActiveLanguages]
	}

	// Leaderboard kinds — empty param.
	leaderboards := []disco.Kind{
		disco.KindTrendingDay,
		disco.KindTrendingWeek,
		disco.KindPopular,
	}
	for _, lang := range langs {
		for _, kind := range leaderboards {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.maybeRefresh(ctx, kind, "", lang, langs)
		}
		// Curated by_genre / by_network — top-10 TMDB ids each.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.maybeRefreshCurated(ctx, disco.KindByGenre, lang, langs)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.maybeRefreshCurated(ctx, disco.KindByNetwork, lang, langs)
	}

	// Warming probe (hotfix): if Tick fired NO refresh because every list
	// was within its freshness window, but data exists from a prior run,
	// flip warming=false so handlers stop returning the cold-start
	// envelope. Without this, a redeploy against an already-populated
	// discovery_lists table leaves IsWarming() == true indefinitely —
	// the original CompareAndSwap inside refresh() only fires on a
	// successful list write, which never happens on a fresh redeploy.
	if !w.warmingOnce.Load() {
		if has, err := w.repo.HasAnyList(ctx); err == nil && has {
			if w.warmingOnce.CompareAndSwap(false, true) {
				observability.SetDiscoveryWarming(false)
				w.log.InfoContext(ctx, "discovery warming flipped via probe",
					slog.String("domain", "discovery"))
			}
		}
	}
	return nil
}

// maybeRefresh runs one (kind, param, lang) refresh if the list is
// stale. Errors are absorbed (logged) so the per-(lang, kind) loop
// in Tick continues.
//
// activeLangs — Story 568 A2. The full set of user languages the outer
// Tick loop is running for. Passed through so the successful
// ReplaceList branch can fan out RefreshSeriesText(force=false) per
// (item, activeLang) pair via preWarmSeriesTexts. Nil / empty →
// pre-warm becomes a no-op (matches Nil PreWarmer semantics).
func (w *Worker) maybeRefresh(ctx context.Context, kind disco.Kind, param, lang string, activeLangs []string) {
	stale, err := w.repo.IsStale(ctx, kind, param, lang, ScheduleFor(kind))
	if err != nil {
		w.log.WarnContext(ctx, "discovery is_stale query failed",
			slog.String("kind", string(kind)),
			slog.String("param", param),
			slog.String("language", lang),
			slog.String("error", err.Error()))
		return
	}
	if !stale {
		return
	}
	if err := w.refresh(ctx, kind, param, lang, activeLangs); err != nil {
		// refresh already logged + bumped the error metric.
		_ = err
	}
}

// maybeRefreshCurated iterates the top-10 ids for kind ∈ {by_genre,
// by_network} and calls maybeRefresh per id. Empty catalog → no work
// (the cold-start chicken-and-egg cover; story 507 on-demand handler
// covers the alternative).
func (w *Worker) maybeRefreshCurated(ctx context.Context, kind disco.Kind, lang string, activeLangs []string) {
	var (
		ids []int
		err error
	)
	switch kind {
	case disco.KindByGenre:
		ids, err = w.topKinds.TopGenres(ctx, TopKindsLimit)
	case disco.KindByNetwork:
		ids, err = w.topKinds.TopNetworks(ctx, TopKindsLimit)
	default:
		return
	}
	if err != nil {
		w.log.WarnContext(ctx, "discovery top_kinds query failed",
			slog.String("kind", string(kind)),
			slog.String("error", err.Error()))
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		w.maybeRefresh(ctx, kind, strconv.Itoa(id), lang, activeLangs)
	}
}

// refresh fetches one (kind, param, lang) list from TMDB and writes
// it through repo.ReplaceList. Per-step errors abort the refresh and
// bump outcome="error"; the OLD data stays in place until the next
// successful Tick.
//
// activeLangs — Story 568 A2. Full active-language set. After a
// successful ReplaceList, the worker fans out preWarmSeriesTexts
// which iterates items × activeLangs and calls
// SeriesTextPreWarmer.PreWarm(force=false) per pair. When
// PreWarmer is nil (config toggle OFF) the fan-out is a no-op.
// activeLangs may be nil / empty for on-demand RefreshNow calls
// (story 507 handler path); the fan-out becomes a no-op in that case.
func (w *Worker) refresh(ctx context.Context, kind disco.Kind, param, lang string, activeLangs []string) error {
	// B-39: pace refresh() so the cold-start sweep doesn't fan-out the full
	// stub-upsert + enrichment-enqueue burst within a few seconds. Wait
	// BEFORE the first TMDB fetch so the limiter accounts for the
	// downstream stub-upsert + ReplaceList work as one unit. ctx
	// cancellation surfaces as the only error path here — RunForever
	// already swallows it on shutdown.
	waitStart := w.clock.Now()
	if err := w.limiter.Wait(ctx); err != nil {
		return err
	}
	observability.ObserveDiscoveryRefreshPaceWait(
		string(kind), lang, w.clock.Now().Sub(waitStart))

	start := w.clock.Now()
	pages := PagesFor(kind)
	items := make([]disco.Item, 0, pages*20)
	for page := 1; page <= pages; page++ {
		resp, err := w.fetchPage(ctx, kind, param, lang, page)
		if err != nil {
			observability.IncDiscoveryRefresh(string(kind), lang, "error")
			w.log.WarnContext(ctx, "discovery list refresh failed",
				slog.String("kind", string(kind)),
				slog.String("param", param),
				slog.String("language", lang),
				slog.Int("page", page),
				slog.String("error", err.Error()))
			return err
		}
		if resp == nil {
			break
		}
		for _, entry := range resp.Results {
			it, ierr := w.materialiseItem(ctx, lang, entry)
			if ierr != nil {
				// Stub-upsert failure for ONE entry must not poison the whole
				// list — skip the row, surface a debug log, continue.
				w.log.DebugContext(ctx, "discovery stub upsert failed",
					slog.String("kind", string(kind)),
					slog.String("language", lang),
					slog.Int64("tmdb_id", entry.ID),
					slog.String("error", ierr.Error()))
				continue
			}
			items = append(items, it)
		}
		// TMDB pages cap at TotalPages; stop early when fewer pages exist.
		if resp.TotalPages > 0 && resp.Page >= resp.TotalPages {
			break
		}
	}

	// PRIMARY KEY (kind, param, language, series_id) — TMDB occasionally
	// returns the same series multiple times across pages of trending /
	// popular. Keep first occurrence (best TMDB rank position).
	items = dedupItemsBySeriesID(items)

	if err := w.repo.ReplaceList(ctx, kind, param, lang, items); err != nil {
		observability.IncDiscoveryRefresh(string(kind), lang, "error")
		w.log.WarnContext(ctx, "discovery replace list failed",
			slog.String("kind", string(kind)),
			slog.String("param", param),
			slog.String("language", lang),
			slog.String("error", err.Error()))
		return err
	}

	duration := w.clock.Now().Sub(start)
	observability.IncDiscoveryRefresh(string(kind), lang, "ok")
	observability.ObserveDiscoveryRefreshDuration(string(kind), lang, duration)
	observability.SetDiscoveryListSize(string(kind), lang, len(items))
	observability.SetDiscoveryListAge(string(kind), lang, 0)

	// Flip warming 1→0 exactly once on first successful list write.
	if w.warmingOnce.CompareAndSwap(false, true) {
		observability.SetDiscoveryWarming(false)
	}

	w.log.InfoContext(ctx, "discovery list refreshed",
		slog.String("kind", string(kind)),
		slog.String("param", param),
		slog.String("language", lang),
		slog.Int("items", len(items)),
		slog.Int64("duration_ms", duration.Milliseconds()))

	// Story 568 A2 — pre-warm series_texts.{seriesID, activeLang} for
	// every item across every active user language. Nil-OK receiver
	// (config toggle OFF) + defensive empty-slice guards mean the call
	// is safe unconditionally.
	w.preWarmSeriesTexts(ctx, kind, param, lang, items, activeLangs)

	return nil
}

// fetchPage dispatches to the right TMDB endpoint per kind.
func (w *Worker) fetchPage(ctx context.Context, kind disco.Kind, param, lang string, page int) (*tmdb.TVListResponse, error) {
	switch kind {
	case disco.KindTrendingDay:
		return w.tmdb.Trending(ctx, tmdb.TrendingDay, lang, page)
	case disco.KindTrendingWeek:
		return w.tmdb.Trending(ctx, tmdb.TrendingWeek, lang, page)
	case disco.KindPopular:
		return w.tmdb.Popular(ctx, lang, page)
	case disco.KindByGenre:
		id, err := strconv.Atoi(param)
		if err != nil {
			return nil, errors.New("by_genre param must be int tmdb id")
		}
		return w.tmdb.DiscoverTV(ctx, tmdb.DiscoverFilter{WithGenres: []int{id}}, page)
	case disco.KindByNetwork:
		id, err := strconv.Atoi(param)
		if err != nil {
			return nil, errors.New("by_network param must be int tmdb id")
		}
		return w.tmdb.DiscoverTV(ctx, tmdb.DiscoverFilter{WithNetworks: []int{id}}, page)
	case disco.KindByKeyword:
		id, err := strconv.Atoi(param)
		if err != nil {
			return nil, errors.New("by_keyword param must be int tmdb id")
		}
		return w.tmdb.DiscoverTV(ctx, tmdb.DiscoverFilter{WithKeywords: []int{id}}, page)
	}
	return nil, errors.New("unknown kind " + string(kind))
}

// materialiseItem converts a TMDB TVListEntry → disco.Item, including
// the stub-upsert side effect (story 505 invariant): any TMDB id not
// in the local series table gets a stub row, and the returned SeriesID
// is the FK the repo.ReplaceList INSERT requires.
func (w *Worker) materialiseItem(ctx context.Context, lang string, entry tmdb.TVListEntry) (disco.Item, error) {
	if entry.ID <= 0 || entry.Name == "" {
		return disco.Item{}, errors.New("entry missing id or name")
	}
	tmdbID := shareddomain.TMDBID(entry.ID)
	posterCopy := entry.PosterPath
	backdropCopy := entry.BackdropPath
	var poster, backdrop *string
	if posterCopy != "" {
		poster = &posterCopy
	}
	if backdropCopy != "" {
		backdrop = &backdropCopy
	}
	sid, err := w.stubs.EnsureStub(ctx, tmdbID, lang, entry.Name, entry.OriginalName, entry.OriginalLanguage, poster, backdrop)
	if err != nil {
		return disco.Item{}, err
	}

	year := parseYear(entry.FirstAirDate)
	countries := append([]string(nil), entry.OriginCountry...)
	tmdbIDCopy := tmdbID
	return disco.Item{
		SeriesID:        sid,
		TMDBID:          &tmdbIDCopy,
		Title:           entry.Name,
		Year:            year,
		PosterPath:      poster,
		BackdropPath:    backdrop,
		OriginCountries: countries,
		Genres:          nil, // handler resolves at projection time (story 507)
		TMDBType:        nil, // TV list entries don't carry tmdb_type
	}, nil
}

// parseYear extracts YYYY from a TMDB first_air_date string ("YYYY-MM-DD"
// or empty). Returns nil on missing / malformed input so the caller's
// disco.Item.Year stays NULL on the DB side.
func parseYear(d string) *int {
	if len(d) < 4 {
		return nil
	}
	y, err := strconv.Atoi(d[:4])
	if err != nil || y < 1900 || y > 2200 {
		return nil
	}
	return &y
}

// dedupItemsBySeriesID removes duplicate items keyed by SeriesID,
// preserving first occurrence order. Defends ReplaceList against the
// discovery_lists PK uniqueness (kind, param, language, series_id) when
// TMDB returns the same id on multiple pages (rare but reproducible
// for trending_day / trending_week).
func dedupItemsBySeriesID(items []disco.Item) []disco.Item {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[shareddomain.SeriesID]struct{}, len(items))
	out := items[:0]
	for _, it := range items {
		if _, dup := seen[it.SeriesID]; dup {
			continue
		}
		seen[it.SeriesID] = struct{}{}
		out = append(out, it)
	}
	return out
}

// IsWarming reports whether the worker has yet completed its first
// successful list refresh. Returns true between boot and the first
// ReplaceList ok-flip; false thereafter. Read by the discovery HTTP
// handlers (story 507) to surface a cold-start envelope on
// /trending /popular requests instead of an empty 200.
//
// The flag is sticky-OFF — once a refresh succeeds the worker never
// flips back to "warming" (a transient TMDB outage downgrades to
// degraded:["tmdb_throttled"] in the handler instead). The atomic
// load is cheap enough to call on every request without contention.
func (w *Worker) IsWarming() bool {
	return !w.warmingOnce.Load()
}

// RefreshNow runs a single (kind, param, lang) refresh synchronously.
// Exposes the private `refresh` for the story 507 on-demand long-tail
// path: a /discovery/genre/{id} request whose list is missing or
// stale-by-7d triggers RefreshNow inline so the response carries
// freshly-fetched items instead of 0 results.
//
// Concurrency: callers MUST de-dupe at the (kind, param, lang) key
// (e.g. golang.org/x/sync/singleflight) — RefreshNow itself does not
// coalesce duplicate calls. The worker's main Tick loop is
// single-threaded, but on-demand requests are concurrent per gin
// request goroutine.
//
// A2 pre-warm: on-demand RefreshNow does NOT fan out the pre-warm
// path — activeLangs is nil, and preWarmSeriesTexts short-circuits on
// an empty slice. Rationale: on-demand callers are latency-sensitive
// (composer sync mode), and A2's pre-warm is designed to piggyback
// on the periodic Tick loop where cost is amortised over the 1h
// cadence. The user click for the on-demand path will land Fresh via
// Freshener's own path when the click lands.
func (w *Worker) RefreshNow(ctx context.Context, kind disco.Kind, param, lang string) error {
	return w.refresh(ctx, kind, param, lang, nil)
}
