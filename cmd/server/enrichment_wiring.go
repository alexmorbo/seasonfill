package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	appenrich "github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	infraextsvc "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
	infraomdb "github.com/alexmorbo/seasonfill/infrastructure/omdb"
	"github.com/alexmorbo/seasonfill/infrastructure/tmdb"
)

// EnrichmentBundle groups the dispatcher + the nightly job closure
// so main.go's wiring stays a single call.
type EnrichmentBundle struct {
	Dispatcher *appenrich.DispatcherImpl
	Nightly    func(context.Context)
	// ColdStart (212) runs the one-shot series backfill — series
	// rows that lack a sync_log(tmdb_series) entry are enqueued at
	// PriorityCold. nil when enrichment is disabled.
	ColdStart func(context.Context)
	// 213 additions: nil when OMDb disabled / unconfigured.
	OMDbDailyBatch  func(context.Context)
	OMDbBudgetReset func(context.Context)
}

// wireEnrichment builds the dispatcher + nightly stale scan closure.
// Returns a nil dispatcher when TMDB is disabled or no token is set
// (boot path stays green on a freshly-installed instance with no
// runtime config yet).
func wireEnrichment(
	rootCtx context.Context,
	extSub *ExternalServicesSubscriber,
	repos enrichmentRepoBundle,
	tx appenrich.Transactor,
	log *slog.Logger,
) (*EnrichmentBundle, error) {
	settings := extSub.Get(infraextsvc.ServiceTMDB)
	if !settings.Enabled || settings.APIKey == "" {
		log.InfoContext(rootCtx, "enrichment.disabled",
			slog.Bool("enabled", settings.Enabled),
			slog.Bool("api_key", settings.APIKey != ""))
		return &EnrichmentBundle{}, nil
	}

	httpClient, err := infraextsvc.HttpClientFor(settings)
	if err != nil {
		return nil, err
	}
	tmdbClient, err := tmdb.New(tmdb.Config{
		Token:      settings.APIKey,
		HTTPClient: httpClient,
		Language:   tmdb.DefaultLanguage,
	})
	if err != nil {
		return nil, err
	}

	// Story 212: dispatcherHolder breaks the construction cycle
	// (series worker needs Dispatcher seam → dispatcher needs both
	// handlers). The holder is handed to the series worker; the
	// real *DispatcherImpl is plugged into it after both workers
	// + dispatcher have been constructed. Calls before the plug-in
	// would no-op safely, but in this flow nothing fires before
	// dispatcher.Start.
	holder := &dispatcherHolder{}

	worker, err := appenrich.NewSeriesWorker(appenrich.SeriesWorkerDeps{
		TMDB:            tmdbClient,
		Tx:              tx,
		Language:        tmdb.DefaultLanguage,
		Series:          repos.Series,
		SeriesTexts:     repos.SeriesTexts,
		Seasons:         repos.Seasons,
		Episodes:        repos.Episodes,
		EpisodeTexts:    repos.EpisodeTexts,
		People:          repos.People,
		SeriesPeople:    repos.SeriesPeople,
		Genres:          repos.Genres,
		Keywords:        repos.Keywords,
		Networks:        repos.Networks,
		Companies:       repos.Companies,
		Videos:          repos.Videos,
		ContentRatings:  repos.ContentRatings,
		ExternalIDs:     repos.ExternalIDs,
		Recommendations: repos.Recommendations,
		SyncLog:         repos.SyncLog,
		MediaPrewarmer:  nil, // F-1 not yet shipped
		Dispatcher:      holder,
		Logger:          log,
	})
	if err != nil {
		return nil, err
	}

	// 212: person worker — REUSES the SAME tmdbClient pointer
	// constructed above. Sharing the pointer preserves the 5-rps
	// token bucket (Client.limiter); a second tmdb.New(...) here
	// would fragment the bucket and let the worker pool burst at
	// 10 rps. NEVER call tmdb.New again in this function.
	personWorker, err := appenrich.NewPersonWorker(appenrich.PersonWorkerDeps{
		TMDB:              tmdbClient,
		Tx:                tx,
		Language:          tmdb.DefaultLanguage,
		People:            repos.People,
		PersonBiographies: repos.PersonBiographies,
		PersonCredits:     repos.PersonCredits,
		ExternalIDs:       repos.ExternalIDs,
		SyncLog:           repos.SyncLog,
		Logger:            log,
	})
	if err != nil {
		return nil, err
	}

	// 213 (D-1) — OMDb client + budget + worker. Best-effort: when
	// OMDb is disabled / unconfigured we leave omdbHolder nil and
	// the cron closure short-circuits. The dispatcher's EntityOMDb
	// goroutine STILL spawns (so a manual enqueue is not silently
	// dropped) but every dequeue logs "handler_nil" because
	// OMDbHandler is wired only when the holder is non-nil.
	var (
		omdbHolder       *omdbClientHolder
		omdbBudget       *appenrich.OMDbBudgetGuard
		omdbWorkerHandle func(context.Context, int64) error
		omdbDailyBatch   func(context.Context)
		omdbBudgetReset  func(context.Context)
	)
	omdbSettings := extSub.Get(infraextsvc.ServiceOMDB)
	if omdbSettings.Enabled && omdbSettings.APIKey != "" {
		omdbHTTPClient, err := infraextsvc.HttpClientFor(omdbSettings)
		if err != nil {
			return nil, fmt.Errorf("omdb http client: %w", err)
		}
		omdbClient, err := infraomdb.New(infraomdb.Config{
			APIKey:     omdbSettings.APIKey,
			HTTPClient: omdbHTTPClient,
		})
		if err != nil {
			return nil, fmt.Errorf("omdb client: %w", err)
		}
		omdbHolder = &omdbClientHolder{inner: omdbClient}
		omdbBudget = appenrich.NewOMDbBudgetGuard(appenrich.DefaultOMDbBudget)

		omdbWorker, err := appenrich.NewOMDbWorker(appenrich.OMDbWorkerDeps{
			Client:  omdbHolder.get,
			Budget:  omdbBudget,
			Tx:      tx,
			Series:  repos.Series,
			SyncLog: repos.SyncLog,
			Logger:  log,
		})
		if err != nil {
			return nil, fmt.Errorf("new omdb worker: %w", err)
		}
		omdbWorkerHandle = omdbWorker.Handle
	} else {
		log.InfoContext(rootCtx, "enrichment.omdb.disabled",
			slog.Bool("enabled", omdbSettings.Enabled),
			slog.Bool("api_key", omdbSettings.APIKey != ""))
	}

	dispatcher := appenrich.NewDispatcher(appenrich.Workers{
		SeriesHandler: worker.Handle,
		PersonHandler: personWorker.Handle,
		OMDbHandler:   omdbWorkerHandle, // nil-OK
	}, log)
	holder.set(dispatcher)

	// 213: Daily-batch + budget-reset closures (cron 04:30 / 04:00).
	// Constructed AFTER dispatcher exists so the batch closure can
	// reference dispatcher.Enqueue. omdbBudget may be nil (OMDb
	// disabled) — closures stay nil and main.go skips Register.
	if omdbBudget != nil {
		omdbDailyBatch = func(ctx context.Context) {
			if repos.LibraryWithIMDB == nil {
				log.WarnContext(ctx, "enrichment.omdb.daily_batch.no_scanner")
				return
			}
			const batchLimit = 900
			ttl := enrichment.TTL(enrichment.SourceOMDb, enrichment.KindOMDb)
			ids, err := repos.LibraryWithIMDB.ListLibraryWithIMDBStale(ctx, ttl, batchLimit)
			if err != nil {
				log.WarnContext(ctx, "enrichment.omdb.daily_batch.scan_failed",
					slog.String("error", err.Error()))
				return
			}
			for _, id := range ids {
				dispatcher.Enqueue(appenrich.EntityOMDb, id, appenrich.PriorityCold)
			}
			log.InfoContext(ctx, "enrichment.omdb.daily_batch.enqueued",
				slog.Int("series_count", len(ids)),
				slog.Int("quota_remaining", omdbBudget.Remaining()))
		}
		omdbBudgetReset = func(ctx context.Context) {
			before := omdbBudget.Remaining()
			omdbBudget.Reset()
			log.InfoContext(ctx, "enrichment.omdb.budget.reset",
				slog.Int("before", before),
				slog.Int("after", omdbBudget.Remaining()))
		}
	}

	nightly := func(ctx context.Context) {
		now := time.Now().UTC()
		// Series sweep: cutoff is now - 2 × continuing-series TTL
		// (24h). Ended-series rows with 30d TTL are still selected
		// since they're already older.
		seriesCutoff := now.Add(-2 * 24 * time.Hour)
		seriesStale, err := repos.SyncLog.StaleScan(ctx, enrichment.SourceTMDBSeries, seriesCutoff, 100)
		if err != nil {
			log.WarnContext(ctx, "enrichment.nightly.stale_scan_failed",
				slog.String("source", string(enrichment.SourceTMDBSeries)),
				slog.String("error", err.Error()))
		}
		seriesRetries, err := repos.SyncLog.RetryDue(ctx, enrichment.SourceTMDBSeries, now, 100)
		if err != nil {
			log.WarnContext(ctx, "enrichment.nightly.retry_due_failed",
				slog.String("source", string(enrichment.SourceTMDBSeries)),
				slog.String("error", err.Error()))
		}
		for _, e := range seriesStale {
			dispatcher.Enqueue(appenrich.EntitySeries, e.EntityID, appenrich.PriorityCold)
		}
		for _, e := range seriesRetries {
			dispatcher.Enqueue(appenrich.EntitySeries, e.EntityID, appenrich.PriorityCold)
		}

		// 212: person sweep — 30d person TTL → cutoff = now - 60d.
		personCutoff := now.Add(-60 * 24 * time.Hour)
		personStale, err := repos.SyncLog.StaleScan(ctx, enrichment.SourceTMDBPerson, personCutoff, 200)
		if err != nil {
			log.WarnContext(ctx, "enrichment.nightly.stale_scan_failed",
				slog.String("source", string(enrichment.SourceTMDBPerson)),
				slog.String("error", err.Error()))
		}
		personRetries, err := repos.SyncLog.RetryDue(ctx, enrichment.SourceTMDBPerson, now, 200)
		if err != nil {
			log.WarnContext(ctx, "enrichment.nightly.retry_due_failed",
				slog.String("source", string(enrichment.SourceTMDBPerson)),
				slog.String("error", err.Error()))
		}
		for _, e := range personStale {
			dispatcher.Enqueue(appenrich.EntityPerson, e.EntityID, appenrich.PriorityCold)
		}
		for _, e := range personRetries {
			dispatcher.Enqueue(appenrich.EntityPerson, e.EntityID, appenrich.PriorityCold)
		}

		log.InfoContext(ctx, "enrichment.nightly.swept",
			slog.Int("series_stale", len(seriesStale)),
			slog.Int("series_retries", len(seriesRetries)),
			slog.Int("person_stale", len(personStale)),
			slog.Int("person_retries", len(personRetries)),
		)
	}

	// 212: cold-start backfill closure. Hands repos.ColdStartScanner
	// + dispatcher to the application-layer function; safe to call
	// from a background goroutine in main.go AFTER dispatcher.Start.
	coldStart := func(ctx context.Context) {
		if repos.ColdStartScanner == nil {
			return
		}
		if err := appenrich.BackfillSeries(ctx, repos.ColdStartScanner, dispatcher, log); err != nil {
			log.WarnContext(ctx, "enrichment.cold_start.failed",
				slog.String("error", err.Error()))
		}
	}

	dispatcher.Start(rootCtx)
	return &EnrichmentBundle{
		Dispatcher:      dispatcher,
		Nightly:         nightly,
		ColdStart:       coldStart,
		OMDbDailyBatch:  omdbDailyBatch,
		OMDbBudgetReset: omdbBudgetReset,
	}, nil
}

// dispatcherHolder is a late-binding holder satisfying
// appenrich.Dispatcher. It exists to break the construction cycle
// between series_worker (needs Dispatcher) and dispatcher (needs
// series worker's Handle). The holder is constructed empty, handed
// to series_worker.deps, and the real dispatcher is plugged in via
// set() AFTER NewDispatcher returns. Concurrency: set() runs
// before dispatcher.Start, so the inner pointer is established
// before any reader goroutine exists.
type dispatcherHolder struct {
	inner appenrich.Dispatcher
}

func (h *dispatcherHolder) set(d appenrich.Dispatcher) { h.inner = d }

func (h *dispatcherHolder) Enqueue(kind appenrich.EntityKind, id int64, p appenrich.Priority) {
	if h.inner == nil {
		return
	}
	h.inner.Enqueue(kind, id, p)
}

func (h *dispatcherHolder) Close() {
	if h.inner == nil {
		return
	}
	h.inner.Close()
}

// enrichmentRepoBundle is the dependency bundle main.go fills in.
// Kept as an explicit struct so the wireEnrichment signature stays
// scannable.
type enrichmentRepoBundle struct {
	Series          appenrich.SeriesRepo
	SeriesTexts     appenrich.SeriesTextsRepo
	Seasons         appenrich.SeasonsRepo
	Episodes        appenrich.EpisodesRepo
	EpisodeTexts    appenrich.EpisodeTextsRepo
	People          peopleRepoCombined
	SeriesPeople    appenrich.SeriesPeopleRepo
	Genres          appenrich.GenresRepo
	Keywords        appenrich.KeywordsRepo
	Networks        appenrich.NetworksRepo
	Companies       appenrich.CompaniesRepo
	Videos          appenrich.VideosRepoPort
	ContentRatings  appenrich.ContentRatingsRepoPort
	ExternalIDs     appenrich.ExternalIDsRepoPort
	Recommendations appenrich.RecommendationsRepoPort
	SyncLog         appenrich.SyncLogRepo
	// 212 additions:
	PersonBiographies appenrich.PersonBiographiesPort
	PersonCredits     appenrich.PersonCreditsPort
	ColdStartScanner  appenrich.ColdStartScanner
	// 213: ListLibraryWithIMDBStale source for the OMDb daily batch.
	// Production impl wraps *SeriesRepository. Nil-OK — when nil the
	// OMDb daily-batch closure logs and short-circuits.
	LibraryWithIMDB OMDbBatchScanner
}

// OMDbBatchScanner is the application-layer surface for the
// "library series with imdb_id whose OMDb sync is stale" query.
// Production impl wraps *SeriesRepository.
type OMDbBatchScanner interface {
	ListLibraryWithIMDBStale(ctx context.Context, ttl time.Duration, limit int) ([]int64, error)
}

// omdbClientHolder is the late-binding holder satisfying the
// appenrich.OMDbWorker getter contract. It exists so the wiring
// layer can swap the underlying *omdb.Client on a future S-2
// reload subscriber without rebuilding the worker. Story 213 only
// constructs the holder once at boot; the reload subscriber lands
// in a follow-up.
type omdbClientHolder struct {
	inner *infraomdb.Client
}

func (h *omdbClientHolder) get() appenrich.OMDbClient {
	if h == nil || h.inner == nil {
		return nil
	}
	return h.inner
}

// set swaps the underlying client. Reserved for the future S-2 reload
// subscriber per Story 213 §10 — not wired in this story.
//
//nolint:unused // wire-up lands with the OMDb reload subscriber follow-up
func (h *omdbClientHolder) set(c *infraomdb.Client) { h.inner = c }

// omdbBatchScannerAdapter wraps *SeriesRepository to satisfy
// OMDbBatchScanner. Out-of-application boundary, no
// imports of infrastructure/database from app.
type omdbBatchScannerAdapter struct {
	inner *repositories.SeriesRepository
}

// NewOMDbBatchScannerAdapter returns the wrapper for main.go's wiring.
func NewOMDbBatchScannerAdapter(s *repositories.SeriesRepository) OMDbBatchScanner {
	return omdbBatchScannerAdapter{inner: s}
}

func (a omdbBatchScannerAdapter) ListLibraryWithIMDBStale(ctx context.Context, ttl time.Duration, limit int) ([]int64, error) {
	return a.inner.ListLibraryWithIMDBStale(ctx, ttl, limit)
}

// peopleRepoCombined is the intersection interface main.go's
// *PeopleRepository must satisfy. The series worker uses PeopleRepo
// (GetByTMDBID + Upsert); the person worker uses PeopleWritePort
// (Get + Upsert). One concrete repo implements both shapes — Go's
// structural typing handles the rest.
type peopleRepoCombined interface {
	appenrich.PeopleRepo
	appenrich.PeopleWritePort
}

// ---- repo → port adapters ------------------------------------------

// videosRepoAdapter wraps *repositories.VideosRepository to satisfy
// VideosRepoPort. The worker's VideoRow uses plain strings; the
// underlying VideoModel persists optional fields as *string —
// translate at this boundary.
type videosRepoAdapter struct {
	inner *repositories.VideosRepository
}

func (a videosRepoAdapter) Upsert(ctx context.Context, v appenrich.VideoRow) error {
	m := repositories.Video{
		SeriesID:    v.SeriesID,
		Name:        v.Name,
		Official:    v.Official,
		PublishedAt: v.PublishedAt,
	}
	if v.TMDBID != "" {
		id := v.TMDBID
		m.TMDBVideoID = &id
	}
	if v.Site != "" {
		s := v.Site
		m.Site = &s
	}
	if v.Key != "" {
		k := v.Key
		m.Key = &k
	}
	if v.Type != "" {
		t := v.Type
		m.Type = &t
	}
	if v.Language != "" {
		l := v.Language
		m.Language = &l
	}
	if m.Name == "" {
		// VideosRepository.Upsert requires a non-empty name. Skip
		// silently — a video with no name has nothing to display.
		return nil
	}
	_, err := a.inner.Upsert(ctx, m)
	return err
}

// contentRatingsRepoAdapter wraps the canonical repo to match the
// (seriesID, country, rating) tuple shape the worker uses.
type contentRatingsRepoAdapter struct {
	inner *repositories.ContentRatingsRepository
}

func (a contentRatingsRepoAdapter) Upsert(ctx context.Context, seriesID int64, country, rating string) error {
	if country == "" || rating == "" {
		return nil
	}
	return a.inner.Upsert(ctx, repositories.ContentRating{
		SeriesID: seriesID, CountryCode: country, Rating: rating,
	})
}

// genresRepoAdapter composes GenresRepository + GenresI18nRepository
// behind the appenrich.GenresRepo port. The port treats the i18n
// write as a single method; the production split is invisible to
// the worker.
type genresRepoAdapter struct {
	main *repositories.GenresRepository
	i18n *repositories.GenresI18nRepository
}

func (a genresRepoAdapter) Upsert(ctx context.Context, g taxonomy.Genre) (int64, error) {
	return a.main.Upsert(ctx, g)
}

func (a genresRepoAdapter) UpsertI18n(ctx context.Context, genreID int64, language, name string) error {
	return a.i18n.Upsert(ctx, taxonomy.GenreI18n{
		GenreID:  genreID,
		Language: language,
		Name:     name,
	})
}

func (a genresRepoAdapter) Set(ctx context.Context, seriesID int64, ids []int64) error {
	return a.main.Set(ctx, seriesID, ids)
}

// keywordsRepoAdapter mirrors genresRepoAdapter.
type keywordsRepoAdapter struct {
	main *repositories.KeywordsRepository
	i18n *repositories.KeywordsI18nRepository
}

func (a keywordsRepoAdapter) Upsert(ctx context.Context, k taxonomy.Keyword) (int64, error) {
	return a.main.Upsert(ctx, k)
}

func (a keywordsRepoAdapter) UpsertI18n(ctx context.Context, keywordID int64, language, name string) error {
	return a.i18n.Upsert(ctx, taxonomy.KeywordI18n{
		KeywordID: keywordID,
		Language:  language,
		Name:      name,
	})
}

func (a keywordsRepoAdapter) Set(ctx context.Context, seriesID int64, ids []int64) error {
	return a.main.Set(ctx, seriesID, ids)
}

// externalIDsRepoAdapter wraps the canonical repo to match the
// (entityType, entityID, provider, value) tuple shape.
type externalIDsRepoAdapter struct {
	inner *repositories.ExternalIDsRepository
}

func (a externalIDsRepoAdapter) Upsert(ctx context.Context, entityType enrichment.EntityType, entityID int64, provider, value string) error {
	if provider == "" || value == "" {
		return nil
	}
	return a.inner.Upsert(ctx, entityType, entityID, provider, value)
}

// ---- 212 adapters --------------------------------------------------

// personCreditsRepoAdapter translates the domain-level
// []people.PersonCredit shape the person worker emits into the
// repository's []database.PersonCreditModel write rows. The domain
// type carries pointer-typed nullable fields (ReleaseDate *time.Time,
// TMDBRating *float64, etc.); the model carries year *int + poster_path
// *string. The conversion lives here so the application layer never
// touches the database package.
type personCreditsRepoAdapter struct {
	inner *repositories.PersonCreditsRepository
}

func (a personCreditsRepoAdapter) BatchUpsert(ctx context.Context, credits []people.PersonCredit) ([]int64, error) {
	if len(credits) == 0 {
		return nil, nil
	}
	rows := make([]database.PersonCreditModel, 0, len(credits))
	for _, c := range credits {
		rows = append(rows, database.PersonCreditModel{
			PersonID:      c.PersonID,
			TMDBCreditID:  c.TMDBCreditID,
			MediaType:     c.MediaType,
			TMDBMediaID:   int(c.TMDBMediaID),
			Title:         c.Title,
			Year:          yearFromReleaseDate(c.ReleaseDate),
			CharacterName: c.CharacterName,
			Kind:          string(c.Kind),
			Job:           c.Job,
			PosterPath:    c.PosterAsset,
			VoteAverage:   c.TMDBRating,
			EpisodeCount:  c.EpisodeCount,
		})
	}
	return a.inner.BatchUpsert(ctx, rows)
}

// yearFromReleaseDate extracts the calendar year from a TMDB release
// date pointer. Used to populate person_credits.year (legacy column
// kept as a denormalised filter index for the H-1 list ordering).
func yearFromReleaseDate(t *time.Time) *int {
	if t == nil {
		return nil
	}
	y := t.Year()
	return &y
}

// coldStartScannerAdapter wraps *SeriesRepository to satisfy
// appenrich.ColdStartScanner. The adapter exists so the application
// port doesn't import infrastructure/database.
type coldStartScannerAdapter struct {
	inner *repositories.SeriesRepository
}

// NewColdStartScannerAdapter returns the wrapper. Kept exported for
// main.go's wiring.
func NewColdStartScannerAdapter(s *repositories.SeriesRepository) appenrich.ColdStartScanner {
	return coldStartScannerAdapter{inner: s}
}

func (a coldStartScannerAdapter) ListMissingSyncLog(ctx context.Context, source string, limit int) ([]int64, error) {
	return a.inner.ListMissingSyncLog(ctx, source, limit)
}
