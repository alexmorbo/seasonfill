package main

import (
	"context"
	"log/slog"
	"time"

	appenrich "github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	infraextsvc "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
	"github.com/alexmorbo/seasonfill/infrastructure/tmdb"
)

// EnrichmentBundle groups the dispatcher + the nightly job closure
// so main.go's wiring stays a single call.
type EnrichmentBundle struct {
	Dispatcher *appenrich.DispatcherImpl
	Nightly    func(context.Context)
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
		Logger:          log,
	})
	if err != nil {
		return nil, err
	}

	dispatcher := appenrich.NewDispatcher(appenrich.Workers{
		SeriesHandler: worker.Handle,
		PersonHandler: nil, // 212 lands this
	}, log)

	nightly := func(ctx context.Context) {
		now := time.Now().UTC()
		// Cutoff: now - 2 × TTL. We use the continuing-series TTL
		// (24h) so cutoff is 48h ago. Ended-series rows with
		// 30d TTL are still selected since they're already older.
		cutoff := now.Add(-2 * 24 * time.Hour)
		stale, err := repos.SyncLog.StaleScan(ctx, enrichment.SourceTMDBSeries, cutoff, 100)
		if err != nil {
			log.WarnContext(ctx, "enrichment.nightly.stale_scan_failed",
				slog.String("error", err.Error()))
			return
		}
		retries, err := repos.SyncLog.RetryDue(ctx, enrichment.SourceTMDBSeries, now, 100)
		if err != nil {
			log.WarnContext(ctx, "enrichment.nightly.retry_due_failed",
				slog.String("error", err.Error()))
		}
		for _, e := range stale {
			dispatcher.Enqueue(appenrich.EntitySeries, e.EntityID, appenrich.PriorityCold)
		}
		for _, e := range retries {
			dispatcher.Enqueue(appenrich.EntitySeries, e.EntityID, appenrich.PriorityCold)
		}
		log.InfoContext(ctx, "enrichment.nightly.swept",
			slog.Int("stale_count", len(stale)),
			slog.Int("retry_count", len(retries)),
		)
	}

	dispatcher.Start(rootCtx)
	return &EnrichmentBundle{
		Dispatcher: dispatcher,
		Nightly:    nightly,
	}, nil
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
	People          appenrich.PeopleRepo
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
