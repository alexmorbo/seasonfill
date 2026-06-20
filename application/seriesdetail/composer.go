// Package seriesdetail — see ports.go header.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Detail is the composer's domain object — the handler maps it
// onto dto.SeriesDetailResponse without further DB queries.
type Detail struct {
	Instance       domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
	SeriesID       domain.SeriesID
	Lang           string

	Canon           series.Canon
	CacheEntry      series.CacheEntry
	Text            *series.SeriesText
	Seasons         []SeasonDetail
	Cast            []CastDetail
	Trailer         *repositories.Video
	ContentRating   *repositories.ContentRating
	ExternalIDs     map[string]string
	Recommendations []RecommendationDetail
	Genres          []taxonomy.Genre
	Keywords        []taxonomy.Keyword
	Networks        []taxonomy.Network
	Companies       []taxonomy.ProductionCompany
	Queue           *QueueRecordDetail
	QueueRecords    []QueueRecordDetail // story 379 — all records
	Torrents        TorrentsPlaceholder
	Recent          []RecentItem
	NextEpisode     *NextEpisodeDetail
	InProgress      *InProgressDetail // story 379 — composer pick
	Degraded        []enrichment.Source
	SyncedAt        time.Time
}

// NextEpisodeDetail — the composer's pick of the earliest future-dated
// non-Specials episode across d.Seasons. Surfaced on Detail so the
// handler can prefer it over canon.next_air_date. Story 373.
type NextEpisodeDetail struct {
	SeasonNumber  int
	EpisodeNumber int
	Title         *string
	AirDate       *time.Time
}

// QueueRecordDetail mirrors the Sonarr queue fields the DTO needs.
// Kept local so the domain object stays independent of the live
// client struct (helps tests).
//
// SonarrEpisodeID is typed (story 407 A-5e) — the prior bare int
// field was the last untyped Sonarr id on the composer path. The
// wire DTO (DownloadChip.EpisodeID) stays plain int; the handler
// converts at the boundary.
type QueueRecordDetail struct {
	QueueID         int
	SonarrEpisodeID domain.SonarrEpisodeID
	SeasonNumber    int
	Title           string
	Status          string
	DownloadID      string
	Protocol        string
	EpisodeNumber   int   // story 379
	Size            int64 // story 379
	SizeLeft        int64 // story 379
}

// SeasonDetail — one season + episodes + per-instance states +
// localised texts, fully composed. Story 377 attaches the persisted
// season-level Sonarr statistics under Stats so mapSeasons does not
// have to walk episode_states (which is empty for seasons skipped by
// scan_skip_handled_seasons).
type SeasonDetail struct {
	Canon    series.CanonSeason
	Episodes []EpisodeDetail
	Stats    *series.SeasonStat
}

// EpisodeDetail — canon episode + state + localised text bundle.
type EpisodeDetail struct {
	Canon series.CanonEpisode
	State *series.EpisodeState
	Text  *series.EpisodeText
}

// CastDetail — one cast credit + the resolved person row.
type CastDetail struct {
	Credit people.SeriesCredit
	Person people.Person
}

// RecommendationDetail — recommended canon row + in-library scope.
type RecommendationDetail struct {
	Series         series.Canon
	InLibrary      bool
	InstanceName   domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
}

// TorrentsPlaceholder — A-* branch placeholder.
type TorrentsPlaceholder struct {
	SyncPending    bool
	Count          int
	TotalSizeBytes int64
}

// RecentItem — placeholder for the recent-activity strip.
// Always empty in this story; see §9 implementation note.
type RecentItem struct {
	EventType string
	Subject   string
	At        time.Time
}

// Deps groups the ports — constructor takes one struct so the
// wiring site stays one block of named fields, not a 19-positional
// argument call.
type Deps struct {
	SeriesCache       SeriesCachePort
	SeriesCacheLookup SeriesCacheLookupPort
	Series            SeriesPort
	SeriesTexts       SeriesTextsPort
	Seasons           SeasonsPort
	Episodes          EpisodesPort
	EpisodeStates     EpisodeStatesPort
	// SeasonStats — story 377. Per-(instance, series, season) Sonarr
	// statistics projection. Nil-OK: when not wired the composer skips
	// the load + the handler falls back to walking episode_states.
	SeasonStats     SeasonStatsPort
	EpisodeTexts    EpisodeTextsPort
	SeriesPeople    SeriesPeoplePort
	People          PeoplePort
	Genres          GenresPort
	Keywords        KeywordsPort
	Networks        NetworksPort
	Companies       CompaniesPort
	Videos          VideosPort
	ContentRatings  ContentRatingsPort
	ExternalIDs     ExternalIDsPort
	Recommendations RecommendationsPort
	SyncLog         SyncLogPort
	SonarrFor       func(instanceName domain.InstanceName) (SonarrQueueLister, bool)
	Logger          *slog.Logger
	Now             func() time.Time
	// MediaResolver translates raw TMDB image paths on canon entities into the
	// sha256 hash the frontend serves via /api/v1/media/:hash. Story 312. Pass
	// NewNopMediaResolver() when the media subsystem is disabled — every wire
	// field falls back to nil and the frontend renders monograms.
	MediaResolver *MediaResolver
}

// Composer is the one application use case for the series detail
// page composite read.
type Composer struct {
	d Deps
}

// NewComposer constructs the composer; Logger defaults to slog.Default,
// Now defaults to time.Now.UTC.
func NewComposer(d Deps) *Composer {
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MediaResolver == nil {
		d.MediaResolver = NewNopMediaResolver()
	}
	return &Composer{d: d}
}

// Get runs the 9-branch composite read for the series detail page.
// `lang` defaults to "en-US" when empty.
func (c *Composer) Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID, lang string) (*Detail, error) {
	lang = resolveLang(lang)
	start := c.d.Now()

	// Step 1 — resolve series_cache → series_id. Failure here is
	// the 404 path; the composer does NOT run the errgroup on a
	// miss (no canon row → nothing to render).
	cache, err := c.d.SeriesCache.Get(ctx, instanceName, sonarrSeriesID)
	if err != nil {
		return nil, fmt.Errorf("series_cache lookup: %w", err)
	}
	if cache.SeriesID == nil || *cache.SeriesID == 0 {
		// Series_cache exists but has no series_id pointer — this
		// would only happen on a broken post-cutover row; treat
		// like 404 so the handler can map it. Carry the typed err so
		// middleware dispatches series_cache_not_found instead of the
		// opaque not_found code.
		return nil, errors.Join(
			&sharedErrors.SeriesCacheNotFoundError{
				InstanceName:   instanceName,
				SonarrSeriesID: sonarrSeriesID,
			},
			ports.ErrNotFound,
		)
	}
	seriesID := *cache.SeriesID

	// Step 2 — canon series row. Same 404 mapping.
	canon, err := c.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("series canon load: %w", err)
	}

	d := &Detail{
		Instance:       instanceName,
		SonarrSeriesID: sonarrSeriesID,
		SeriesID:       seriesID,
		Lang:           lang,
		Canon:          canon,
		CacheEntry:     cache,
		Torrents:       TorrentsPlaceholder{SyncPending: true},
		ExternalIDs:    map[string]string{},
	}

	// Branch outcome tracker (composer-local so we can attribute
	// each failure to a source without coupling to enrichment.Source).
	branches := newBranchTracker()

	g, gctx := errgroup.WithContext(ctx)

	// Branch a — series_texts (lang fallback).
	g.Go(func() error {
		return branches.run("series_texts", enrichment.SourceTMDBSeries, c.d.Logger, func() error {
			t, terr := c.d.SeriesTexts.GetWithFallback(gctx, seriesID, lang)
			if terr != nil {
				if errors.Is(terr, ports.ErrNotFound) {
					return nil // not an error — cold series
				}
				return terr
			}
			d.Text = &t
			return nil
		})
	})

	// Branch b — seasons + episodes + episode_texts + episode_states.
	g.Go(func() error {
		return branches.run("seasons_episodes", enrichment.SourceTMDBSeason, c.d.Logger, func() error {
			return c.loadSeasonsAndEpisodes(gctx, d, lang)
		})
	})

	// Branch c — top-10 cast.
	g.Go(func() error {
		return branches.run("cast", enrichment.SourceTMDBSeries, c.d.Logger, func() error {
			return c.loadTopCast(gctx, d, 10)
		})
	})

	// Branch d — taxonomy (genres, keywords, networks, companies).
	g.Go(func() error {
		return branches.run("taxonomy", enrichment.SourceTMDBSeries, c.d.Logger, func() error {
			return c.loadTaxonomy(gctx, d, lang)
		})
	})

	// Branch e — best trailer.
	g.Go(func() error {
		return branches.run("trailer", enrichment.SourceTMDBSeries, c.d.Logger, func() error {
			return c.loadBestTrailer(gctx, d)
		})
	})

	// Branch f — content ratings + external ids.
	g.Go(func() error {
		return branches.run("ratings_ids", enrichment.SourceTMDBSeries, c.d.Logger, func() error {
			return c.loadRatingsAndIDs(gctx, d, lang)
		})
	})

	// Branch g — recommendations.
	g.Go(func() error {
		return branches.run("recommendations", enrichment.SourceTMDBSeries, c.d.Logger, func() error {
			return c.loadRecommendations(gctx, d)
		})
	})

	// Branch h — torrents (placeholder until A-* branch).
	g.Go(func() error {
		c.d.Logger.DebugContext(gctx, "torrents_placeholder",
			slog.String("instance_name", string(instanceName)),
			slog.Int("sonarr_series_id", int(sonarrSeriesID)),
			slog.String("note", "qbit-deferred"))
		// Always succeeds — placeholder doesn't fail the response.
		return nil
	})

	// Branch i — Sonarr Queue (live).
	g.Go(func() error {
		return branches.runLive("sonarr_queue", enrichment.SourceSonarr, c.d.Logger, func() error {
			return c.loadSonarrQueue(gctx, d)
		})
	})

	// Recent activity — placeholder (§9 note).
	c.d.Logger.DebugContext(ctx, "recent_activity_deferred",
		slog.Int64("series_id", int64(seriesID)))
	d.Recent = []RecentItem{}

	// errgroup.Wait — branches NEVER return errors (run/runLive
	// swallow them), so Wait returns nil. We keep the call so
	// goroutine fan-in is deterministic.
	_ = g.Wait()

	// Story 373 — pick the next episode from d.Seasons after all branches
	// have populated it. Runs synchronously post-wait so it sees the final
	// seasons slice; cheap (O(episodes)) and never fails.
	d.NextEpisode = pickNextEpisode(d, c.d.Now())
	// Story 379 — pick the best in-flight Sonarr queue record for the
	// LibraryStrip in-progress pill + per-season chip. Runs post-wait so
	// d.QueueRecords is populated; nil-safe + never fails.
	d.InProgress = pickInProgress(d)

	// Degraded computation: walk sync_log for the four enrichment
	// sources, then call enrichment.Degraded with the branch-failure
	// flags from the tracker.
	d.Degraded, _ = c.computeDegraded(ctx, seriesID, canon, branches)
	d.SyncedAt = c.d.Now()

	// Story 312: translate raw TMDB paths into sha256 hashes via the
	// media_assets index. Misses leave the field nil — frontend renders a
	// monogram fallback. Pure projection; never fails the request.
	c.resolveAssets(ctx, d)

	c.d.Logger.InfoContext(ctx, "series_detail_composed",
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrSeriesID)),
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		slog.Any("degraded", sourceStrings(d.Degraded)),
	)
	return d, nil
}

// GetSeason — same shape as Get, scoped to one season. Used by the
// SPA's seasons-accordion polling. Internally calls
// loadSeasonsAndEpisodes filtered to the single season + the
// canon series for the parent ids.
func (c *Composer) GetSeason(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID, seasonNumber int, lang string) (*Detail, error) {
	lang = resolveLang(lang)
	start := c.d.Now()

	cache, err := c.d.SeriesCache.Get(ctx, instanceName, sonarrSeriesID)
	if err != nil {
		return nil, fmt.Errorf("series_cache lookup: %w", err)
	}
	if cache.SeriesID == nil || *cache.SeriesID == 0 {
		// Preserve typed chain — see Get() comment for rationale.
		return nil, errors.Join(
			&sharedErrors.SeriesCacheNotFoundError{
				InstanceName:   instanceName,
				SonarrSeriesID: sonarrSeriesID,
			},
			ports.ErrNotFound,
		)
	}
	seriesID := *cache.SeriesID
	canon, err := c.d.Series.Get(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("series canon load: %w", err)
	}
	d := &Detail{
		Instance: instanceName, SonarrSeriesID: sonarrSeriesID,
		SeriesID: seriesID, Lang: lang,
		Canon: canon, CacheEntry: cache,
	}
	branches := newBranchTracker()
	_ = branches.run("season_episodes", enrichment.SourceTMDBSeason, c.d.Logger, func() error {
		return c.loadSeasonsAndEpisodes(ctx, d, lang)
	})
	// Filter to the requested season.
	filtered := make([]SeasonDetail, 0, 1)
	for _, s := range d.Seasons {
		if s.Canon.SeasonNumber == seasonNumber {
			filtered = append(filtered, s)
			break
		}
	}
	d.Seasons = filtered
	d.Degraded, _ = c.computeDegraded(ctx, seriesID, canon, branches)
	d.SyncedAt = c.d.Now()
	c.resolveAssets(ctx, d)
	c.d.Logger.InfoContext(ctx, "series_season_composed",
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrSeriesID)),
		slog.Int64("series_id", int64(seriesID)),
		slog.Int("season_number", seasonNumber),
		slog.String("lang", lang),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	if len(filtered) == 0 {
		// Preserve typed chain so middleware can dispatch season_not_found.
		return d, errors.Join(
			&sharedErrors.SeasonNotFoundError{
				InstanceName:   instanceName,
				SonarrSeriesID: sonarrSeriesID,
				SeasonNumber:   seasonNumber,
			},
			ports.ErrNotFound,
		)
	}
	return d, nil
}

// --- branch implementations ---

func (c *Composer) loadSeasonsAndEpisodes(ctx context.Context, d *Detail, lang string) error {
	seasons, err := c.d.Seasons.ListBySeries(ctx, d.SeriesID)
	if err != nil {
		return fmt.Errorf("list seasons: %w", err)
	}
	episodes, err := c.d.Episodes.ListBySeries(ctx, d.SeriesID)
	if err != nil {
		return fmt.Errorf("list episodes: %w", err)
	}
	states, err := c.d.EpisodeStates.ListBySeries(ctx, d.Instance, d.SeriesID)
	if err != nil {
		// per-instance file state — failure surfaces as no-on-disk
		// rendering, NOT a request failure.
		c.d.Logger.WarnContext(ctx, "episode_states_failed",
			slog.String("instance_name", string(d.Instance)),
			slog.Int64("series_id", int64(d.SeriesID)),
			slog.String("error", err.Error()))
		states = nil
	}
	stateByEpID := make(map[domain.EpisodeID]series.EpisodeState, len(states))
	for _, st := range states {
		stateByEpID[st.EpisodeID] = st
	}

	// Story 377: per-season stats projection. Nil port disables the
	// load — handler falls back to walking episode_states. ListBySeries
	// failure is degraded silently (warn-logged, empty map) for the
	// same reason episode_states is: per-instance projection failure
	// should NOT 5xx the composer.
	statsBySeason := map[int]series.SeasonStat{}
	if c.d.SeasonStats != nil {
		stats, serr := c.d.SeasonStats.ListBySeries(ctx, d.Instance, d.SonarrSeriesID)
		if serr != nil {
			c.d.Logger.WarnContext(ctx, "season_stats_failed",
				slog.String("instance_name", string(d.Instance)),
				slog.Int("sonarr_series_id", int(d.SonarrSeriesID)),
				slog.String("error", serr.Error()))
		} else {
			for _, st := range stats {
				statsBySeason[st.SeasonNumber] = st
			}
		}
	}

	// Group episodes by season number.
	bySeason := make(map[int][]series.CanonEpisode)
	for _, e := range episodes {
		bySeason[e.SeasonNumber] = append(bySeason[e.SeasonNumber], e)
	}
	out := make([]SeasonDetail, 0, len(seasons))
	for _, s := range seasons {
		eps := bySeason[s.SeasonNumber]
		sort.Slice(eps, func(i, j int) bool {
			return eps[i].EpisodeNumber < eps[j].EpisodeNumber
		})
		epDetails := make([]EpisodeDetail, 0, len(eps))
		for _, e := range eps {
			ed := EpisodeDetail{Canon: e}
			if st, ok := stateByEpID[domain.EpisodeID(e.ID)]; ok {
				stCopy := st
				ed.State = &stCopy
			}
			// Per-row i18n fallback. For 215 we do N calls — see
			// §3 SeasonsPort note (collapse into one batched JOIN
			// is a follow-up performance story).
			t, terr := c.d.EpisodeTexts.GetWithFallback(ctx, domain.EpisodeID(e.ID), lang)
			if terr == nil {
				et := t
				ed.Text = &et
			}
			epDetails = append(epDetails, ed)
		}
		sd := SeasonDetail{Canon: s, Episodes: epDetails}
		if st, ok := statsBySeason[s.SeasonNumber]; ok {
			stCopy := st
			sd.Stats = &stCopy
		}
		out = append(out, sd)
	}
	d.Seasons = out
	return nil
}

func (c *Composer) loadTopCast(ctx context.Context, d *Detail, limit int) error {
	credits, err := c.d.SeriesPeople.ListBySeries(ctx, d.SeriesID, people.SeriesCreditCast)
	if err != nil {
		return fmt.Errorf("list series_people: %w", err)
	}
	// ListBySeries returns ORDER BY kind ASC, credit_order ASC —
	// already sorted; just slice.
	if len(credits) > limit {
		credits = credits[:limit]
	}
	if len(credits) == 0 {
		d.Cast = nil
		return nil
	}
	ids := make([]int64, 0, len(credits))
	for _, cr := range credits {
		ids = append(ids, cr.PersonID)
	}
	persons, err := c.d.People.ListByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("list people by ids: %w", err)
	}
	byID := make(map[int64]people.Person, len(persons))
	for _, p := range persons {
		byID[p.ID] = p
	}
	d.Cast = make([]CastDetail, 0, len(credits))
	for _, cr := range credits {
		p, ok := byID[cr.PersonID]
		if !ok {
			// Person row missing — credit references an unhydrated
			// stub. Skip (cast list shrinks gracefully).
			continue
		}
		d.Cast = append(d.Cast, CastDetail{Credit: cr, Person: p})
	}
	return nil
}

func (c *Composer) loadTaxonomy(ctx context.Context, d *Detail, lang string) error {
	genreIDs, err := c.d.Genres.ListBySeries(ctx, d.SeriesID)
	if err != nil {
		return fmt.Errorf("list series_genres: %w", err)
	}
	for _, id := range genreIDs {
		g, gerr := c.d.Genres.Get(ctx, id, lang)
		if gerr == nil {
			d.Genres = append(d.Genres, g)
		}
	}
	kwIDs, err := c.d.Keywords.ListBySeries(ctx, d.SeriesID)
	if err != nil {
		return fmt.Errorf("list series_keywords: %w", err)
	}
	for _, id := range kwIDs {
		k, kerr := c.d.Keywords.Get(ctx, id, lang)
		if kerr == nil {
			d.Keywords = append(d.Keywords, k)
		}
	}
	netIDs, err := c.d.Networks.ListBySeries(ctx, d.SeriesID)
	if err != nil {
		return fmt.Errorf("list series_networks: %w", err)
	}
	if len(netIDs) > 0 {
		nets, nerr := c.d.Networks.ListByIDs(ctx, netIDs)
		if nerr != nil {
			return fmt.Errorf("list networks by ids: %w", nerr)
		}
		d.Networks = nets
	}
	coIDs, cerr := c.d.Companies.ListBySeries(ctx, d.SeriesID)
	if cerr == nil && len(coIDs) > 0 {
		cos, _ := c.d.Companies.ListByIDs(ctx, coIDs)
		d.Companies = cos
	}
	return nil
}

func (c *Composer) loadBestTrailer(ctx context.Context, d *Detail) error {
	videos, err := c.d.Videos.ListBySeriesAndType(ctx, d.SeriesID, "Trailer")
	if err != nil {
		return fmt.Errorf("list videos: %w", err)
	}
	// "Best": site=YouTube, official=true, ORDER BY published_at DESC.
	var best *repositories.Video
	for i := range videos {
		v := videos[i]
		if v.Site == nil || !strings.EqualFold(*v.Site, "YouTube") {
			continue
		}
		if !v.Official {
			continue
		}
		if best == nil || (v.PublishedAt != nil && (best.PublishedAt == nil || v.PublishedAt.After(*best.PublishedAt))) {
			vCopy := v
			best = &vCopy
		}
	}
	d.Trailer = best
	return nil
}

func (c *Composer) loadRatingsAndIDs(ctx context.Context, d *Detail, lang string) error {
	ratings, err := c.d.ContentRatings.ListBySeries(ctx, d.SeriesID)
	if err != nil {
		return fmt.Errorf("list content_ratings: %w", err)
	}
	d.ContentRating = pickContentRating(ratings, lang)
	xids, err := c.d.ExternalIDs.ListByEntity(ctx, enrichment.EntityTypeSeries, int64(d.SeriesID))
	if err != nil {
		return fmt.Errorf("list external_ids: %w", err)
	}
	for _, x := range xids {
		d.ExternalIDs[x.Provider] = x.Value
	}
	return nil
}

func (c *Composer) loadRecommendations(ctx context.Context, d *Detail) error {
	ids, err := c.d.Recommendations.ListBySeries(ctx, d.SeriesID)
	if err != nil {
		return fmt.Errorf("list recommendations: %w", err)
	}
	for _, recID := range ids {
		s, sgerr := c.d.Series.Get(ctx, recID)
		if sgerr != nil {
			continue // recommendation stub not yet hydrated
		}
		rd := RecommendationDetail{Series: s}
		// in_library probe: any cache row referencing this series.id?
		caches, _ := c.d.SeriesCacheLookup.ListBySeriesID(ctx, recID)
		if len(caches) > 0 {
			rd.InLibrary = true
			rd.InstanceName = caches[0].InstanceName
			rd.SonarrSeriesID = caches[0].SonarrSeriesID
		}
		d.Recommendations = append(d.Recommendations, rd)
	}
	return nil
}

func (c *Composer) loadSonarrQueue(ctx context.Context, d *Detail) error {
	if c.d.SonarrFor == nil {
		return fmt.Errorf("sonarr client lookup not wired")
	}
	client, ok := c.d.SonarrFor(d.Instance)
	if !ok || client == nil {
		return fmt.Errorf("sonarr unreachable for instance %s", d.Instance)
	}
	q, err := client.Queue(ctx, d.SonarrSeriesID)
	if err != nil {
		return fmt.Errorf("sonarr queue: %w", err)
	}
	if len(q.Records) == 0 {
		return nil
	}
	d.QueueRecords = make([]QueueRecordDetail, 0, len(q.Records))
	for _, rec := range q.Records {
		d.QueueRecords = append(d.QueueRecords, QueueRecordDetail{
			QueueID:         rec.ID,
			SonarrEpisodeID: domain.SonarrEpisodeID(rec.EpisodeID),
			EpisodeNumber:   rec.EpisodeNumber,
			SeasonNumber:    rec.SeasonNumber,
			Title:           rec.Title,
			Status:          rec.Status,
			DownloadID:      rec.DownloadID,
			Protocol:        rec.Protocol,
			Size:            rec.Size,
			SizeLeft:        rec.SizeLeft,
		})
	}
	first := d.QueueRecords[0]
	d.Queue = &first
	return nil
}

// computeDegraded — single source of truth for the degraded[] list.
// Reads sync_log for the four canon sources, applies §5.6 rules
// via enrichment.Degraded, then ORs in the branch-failure flags
// from the tracker.
func (c *Composer) computeDegraded(ctx context.Context, seriesID domain.SeriesID, canon series.Canon, br *branchTracker) ([]enrichment.Source, error) {
	in := enrichment.DegradedInput{
		Logs:            map[enrichment.Source]*enrichment.SyncLog{},
		TTLs:            map[enrichment.Source]time.Duration{},
		SonarrReachable: !br.failed(enrichment.SourceSonarr),
		QbitReachable:   true, // qBit branch is placeholder; assume reachable.
	}
	sources := []enrichment.Source{
		enrichment.SourceTMDBSeries,
		enrichment.SourceTMDBSeason,
		enrichment.SourceTMDBPerson,
		enrichment.SourceOMDb,
	}
	kind := classifyKind(canon)
	for _, s := range sources {
		log, err := c.d.SyncLog.GetLastSync(ctx, enrichment.EntityTypeSeries, int64(seriesID), s)
		switch {
		case err == nil:
			row := log
			in.Logs[s] = &row
		case errors.Is(err, ports.ErrNotFound):
			in.Logs[s] = nil
		default:
			c.d.Logger.WarnContext(ctx, "sync_log_lookup_failed",
				slog.String("source", string(s)),
				slog.Int64("series_id", int64(seriesID)),
				slog.String("error", err.Error()))
			in.Logs[s] = nil
		}
		in.TTLs[s] = enrichment.TTL(s, kindFor(s, kind))
	}
	out := enrichment.Degraded(in, c.d.Now())
	// OR in any branch failure that maps to a TMDB-series source
	// (cast / taxonomy / trailer / recommendations / ratings_ids).
	if br.failed(enrichment.SourceTMDBSeries) && !contains(out, enrichment.SourceTMDBSeries) {
		out = append(out, enrichment.SourceTMDBSeries)
	}
	if br.failed(enrichment.SourceTMDBSeason) && !contains(out, enrichment.SourceTMDBSeason) {
		out = append(out, enrichment.SourceTMDBSeason)
	}
	return out, nil
}

// --- helpers ---

func resolveLang(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return "en-US"
	}
	// Defensive guard against absurd lengths — real BCP-47 tags
	// cap at ~10 chars.
	if len(lang) > 35 {
		return "en-US"
	}
	return lang
}

func pickContentRating(rs []repositories.ContentRating, lang string) *repositories.ContentRating {
	// Pick by user-locale country prefix (lang="ru-RU" → "RU"),
	// then en-US ("US"), then first available. v1 keeps the
	// matching naive — composer correctness over locale subtlety.
	country := ""
	if i := strings.Index(lang, "-"); i > 0 {
		country = strings.ToUpper(lang[i+1:])
	}
	var pickUS *repositories.ContentRating
	for i := range rs {
		r := rs[i]
		if country != "" && r.CountryCode == country {
			return &r
		}
		if r.CountryCode == "US" && pickUS == nil {
			rCopy := r
			pickUS = &rCopy
		}
	}
	if pickUS != nil {
		return pickUS
	}
	if len(rs) > 0 {
		first := rs[0]
		return &first
	}
	return nil
}

// classifyKind picks the TTL Kind bucket for the canon series.
// Returns a coarse "is the series in production?" classification;
// kindFor below specialises per-source.
func classifyKind(c series.Canon) enrichment.Kind {
	if c.InProduction {
		return enrichment.KindSeriesContinuing
	}
	if c.Status != nil {
		st := strings.ToLower(*c.Status)
		if strings.Contains(st, "continu") || strings.Contains(st, "ongoing") {
			return enrichment.KindSeriesContinuing
		}
		if strings.Contains(st, "ended") || strings.Contains(st, "cancel") {
			return enrichment.KindSeriesEnded
		}
	}
	return enrichment.KindSeriesContinuing // default-conservative
}

func kindFor(s enrichment.Source, base enrichment.Kind) enrichment.Kind {
	switch s {
	case enrichment.SourceTMDBSeries:
		return base
	case enrichment.SourceTMDBSeason:
		if base == enrichment.KindSeriesEnded {
			return enrichment.KindSeasonClosed
		}
		return enrichment.KindSeasonActive
	case enrichment.SourceTMDBPerson:
		return enrichment.KindPerson
	case enrichment.SourceOMDb:
		return enrichment.KindOMDb
	}
	return enrichment.KindUnknown
}

// resolveAssets walks every image field on the composed Detail and overwrites
// the raw TMDB path with the sha256 hash from media_assets (where a stored
// row exists). Fields with no stored row are nil → frontend renders a
// monogram. Story 312. Sizes mirror application/enrichment.composePrewarmAssets
// so the resolver looks up the same source URLs the worker enqueues.
func (c *Composer) resolveAssets(ctx context.Context, d *Detail) {
	r := c.d.MediaResolver
	// Story 316 — hero gets a 3s wall budget for synchronous on-demand
	// fetch. Past the budget, ResolveSync short-circuits and falls back
	// to the async path (still gets the priority enqueue).
	syncCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// First-fold (sync): hero poster + backdrop.
	d.Canon.PosterAsset = r.ResolveSync(syncCtx, d.Canon.PosterAsset, "w342", "poster_w342")
	d.Canon.BackdropAsset = r.ResolveSync(syncCtx, d.Canon.BackdropAsset, "w1280", "backdrop_w1280")
	// Rest (async — priority enqueue, no wait).
	for i := range d.Networks {
		d.Networks[i].LogoAsset = r.Resolve(ctx, d.Networks[i].LogoAsset, "w185", "network_logo_w185")
	}
	for i := range d.Seasons {
		d.Seasons[i].Canon.PosterAsset = r.Resolve(ctx, d.Seasons[i].Canon.PosterAsset, "w154", "season_poster_w154")
		for j := range d.Seasons[i].Episodes {
			d.Seasons[i].Episodes[j].Canon.StillAsset = r.Resolve(ctx, d.Seasons[i].Episodes[j].Canon.StillAsset, "w300", "still_w300")
		}
	}
	for i := range d.Cast {
		d.Cast[i].Person.ProfileAsset = r.Resolve(ctx, d.Cast[i].Person.ProfileAsset, "w185", "profile_w185")
	}
	for i := range d.Recommendations {
		d.Recommendations[i].Series.PosterAsset = r.Resolve(ctx, d.Recommendations[i].Series.PosterAsset, "w342", "poster_w342")
	}
}

func contains(s []enrichment.Source, v enrichment.Source) bool {
	return slices.Contains(s, v)
}

func sourceStrings(s []enrichment.Source) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		out = append(out, string(v))
	}
	return out
}

// pickNextEpisode returns the earliest future-dated non-Specials episode
// across d.Seasons. Prefers monitored episodes; falls back to any episode
// when no monitored future episode exists. Returns nil when no future
// episode is present — caller (mapHero) may then fall back to
// d.Canon.NextAirDate. Story 373.
//
// Ties are broken by (air_date, season_number, episode_number) ASC so the
// pick stays deterministic across composer runs.
func pickNextEpisode(d *Detail, now time.Time) *NextEpisodeDetail {
	if d == nil || len(d.Seasons) == 0 {
		return nil
	}
	var bestMonitored, bestAny *NextEpisodeDetail
	for _, s := range d.Seasons {
		// Specials (S0) are TBA-by-nature; skip them for the next-airing card.
		if s.Canon.SeasonNumber <= 0 {
			continue
		}
		for _, ep := range s.Episodes {
			if ep.Canon.AirDate == nil {
				continue
			}
			if !ep.Canon.AirDate.After(now) {
				continue
			}
			cand := &NextEpisodeDetail{
				SeasonNumber:  ep.Canon.SeasonNumber,
				EpisodeNumber: ep.Canon.EpisodeNumber,
				AirDate:       ep.Canon.AirDate,
			}
			if ep.Text != nil && ep.Text.Title != nil && *ep.Text.Title != "" {
				cand.Title = ep.Text.Title
			}
			if bestAny == nil || isEarlier(cand, bestAny) {
				bestAny = cand
			}
			monitored := ep.State != nil && ep.State.Monitored
			if monitored {
				if bestMonitored == nil || isEarlier(cand, bestMonitored) {
					bestMonitored = cand
				}
			}
		}
	}
	if bestMonitored != nil {
		return bestMonitored
	}
	return bestAny
}

// isEarlier — composite (air_date, season, episode) ASC comparator used by
// pickNextEpisode. a.AirDate / b.AirDate are guaranteed non-nil by callers.
func isEarlier(a, b *NextEpisodeDetail) bool {
	if a.AirDate.Before(*b.AirDate) {
		return true
	}
	if a.AirDate.After(*b.AirDate) {
		return false
	}
	if a.SeasonNumber != b.SeasonNumber {
		return a.SeasonNumber < b.SeasonNumber
	}
	return a.EpisodeNumber < b.EpisodeNumber
}

// InProgressDetail — composer's pick of the best in-flight Sonarr queue
// record. Story 379. Surfaced on Detail so mapLibrary projects it onto
// LibraryStrip.in_progress for the hero pill.
type InProgressDetail struct {
	SeasonNumber  int
	EpisodeNumber int
	Title         *string
	Percent       int
}

// pickInProgress — story 379. Returns nil when no record has
// status=="downloading". Picks the highest-percent record; ties broken by
// (season ASC, episode ASC) so the pick stays deterministic.
func pickInProgress(d *Detail) *InProgressDetail {
	if d == nil || len(d.QueueRecords) == 0 {
		return nil
	}
	var best *InProgressDetail
	for _, rec := range d.QueueRecords {
		if rec.Status != "downloading" {
			continue
		}
		cand := &InProgressDetail{
			SeasonNumber:  rec.SeasonNumber,
			EpisodeNumber: rec.EpisodeNumber,
			Percent:       computePercent(rec.Size, rec.SizeLeft),
		}
		if rec.Title != "" {
			t := rec.Title
			cand.Title = &t
		}
		if best == nil || isMoreProgressed(cand, best) {
			best = cand
		}
	}
	return best
}

// computePercent — (size − sizeleft) / size rounded to integer 0..100.
// Returns 0 when upstream reports zero size or partial progress is
// negative (Sonarr edge case).
func computePercent(size, sizeLeft int64) int {
	if size <= 0 {
		return 0
	}
	done := size - sizeLeft
	if done <= 0 {
		return 0
	}
	p := int((done*100 + size/2) / size)
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// isMoreProgressed — story 379 comparator. Highest percent wins; tie
// broken by season ASC then episode ASC.
func isMoreProgressed(a, b *InProgressDetail) bool {
	if a.Percent != b.Percent {
		return a.Percent > b.Percent
	}
	if a.SeasonNumber != b.SeasonNumber {
		return a.SeasonNumber < b.SeasonNumber
	}
	return a.EpisodeNumber < b.EpisodeNumber
}

// --- branchTracker ---

// branchTracker records per-branch outcomes so degraded computation
// can attribute failures to enrichment sources. Concurrent-safe.
type branchTracker struct {
	mu       sync.Mutex
	failures map[enrichment.Source]bool
}

func newBranchTracker() *branchTracker {
	return &branchTracker{failures: map[enrichment.Source]bool{}}
}

// run wraps a branch function; failures are logged + recorded
// against the supplied source but never returned (errgroup wait
// must not abort sibling branches on this story's failure model).
func (b *branchTracker) run(name string, src enrichment.Source, log *slog.Logger, fn func() error) error {
	defer func() {
		if r := recover(); r != nil {
			b.mark(src)
			log.Error("branch_panic",
				slog.String("branch", name),
				slog.Any("recover", r))
		}
	}()
	if err := fn(); err != nil {
		b.mark(src)
		log.Warn("branch_degraded",
			slog.String("branch", name),
			slog.String("source", string(src)),
			slog.String("outcome", "degraded"),
			slog.String("error", err.Error()))
		return nil
	}
	log.Debug("branch_ok",
		slog.String("branch", name),
		slog.String("outcome", "ok"))
	return nil
}

// runLive is the same as run but tagged for live sources (Sonarr,
// qBit) — separate name keeps logs greppable.
func (b *branchTracker) runLive(name string, src enrichment.Source, log *slog.Logger, fn func() error) error {
	return b.run(name, src, log, fn)
}

func (b *branchTracker) mark(s enrichment.Source) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures[s] = true
}

func (b *branchTracker) failed(s enrichment.Source) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures[s]
}
