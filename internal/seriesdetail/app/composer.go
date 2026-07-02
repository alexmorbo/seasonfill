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

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
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
	Trailer         *enrichpersistence.Video
	ContentRating   *enrichpersistence.ContentRating
	ExternalIDs     map[string]string
	Recommendations []RecommendationDetail
	Genres          []taxonomy.Genre
	Keywords        []taxonomy.Keyword
	Networks        []taxonomy.Network
	Companies       []taxonomy.ProductionCompany
	QueueRecords    []QueueRecordDetail // story 379 — all records
	Torrents        TorrentsPlaceholder
	Recent          []RecentItem
	Degraded        []enrichment.Source
	SyncedAt        time.Time
	// InLibraryInstances — sorted list of instance names that carry this
	// series. Populated by GlobalComposerUseCase post-Composer.Get; the
	// per-instance Composer.Get path sets a single-element list (the
	// instance the request hit) for wire-shape parity with the global
	// endpoint. Story 491 / N-1a.
	InLibraryInstances []domain.InstanceName
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
	Freshness       EnrichmentFreshnessPort
	SonarrFor       func(instanceName domain.InstanceName) (SonarrQueueLister, bool)
	Logger          *slog.Logger
	Now             func() time.Time
	// MediaResolver translates raw TMDB image paths on canon entities into the
	// sha256 hash the frontend serves via /api/v1/media/:hash. Story 312. Pass
	// media.NewNopResolver() when the media subsystem is disabled — every wire
	// field falls back to nil and the frontend renders monograms.
	MediaResolver *media.Resolver
	// Freshener (Story 533) — synchronous read-through TMDB refresh on
	// cold/stale per-instance detail open. nil-OK: when nil, Composer.Get
	// reads whatever is currently in the DB (Story 532 behaviour).
	Freshener SeriesFreshener
}

// CastDefaultLimit caps the cast rows returned by
// Composer.GetCanonicalCast and the per-instance Composer.Get
// loadTopCast branch (Story 215). Promoted to an exported constant
// (Story 533a) so the TMDB-fallback call site reads the same number.
const CastDefaultLimit = 10

// CastFullPageDefaultLimit caps the cast page surface
// (GET /series/:id/cast). DB carries up to ~1500 credits per popular
// series (Rick & Morty: 1411). The hero-carousel CastDefaultLimit=10 is
// intentionally compact UX inside /series/:id; the dedicated cast PAGE
// must return the full list. 200 matches TMDB's per-series upper bound
// and keeps DTO payloads ≤ ~60KB. Story 541.
const CastFullPageDefaultLimit = 200

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
		d.MediaResolver = media.NewNopResolver()
	}
	return &Composer{d: d}
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

	// Story 550 (E-1 Z1) — batch i18n fallback. The previous shape
	// issued one episode_texts SELECT per episode (~600 queries for
	// SVU). One JOIN'd query now resolves the full language→en-US
	// fallback chain. Episodes absent from the map have neither a
	// `lang` nor 'en-US' row; the composer mirrors the prior
	// ErrNotFound branch by leaving EpisodeDetail.Text nil.
	episodeIDs := make([]domain.EpisodeID, 0, len(episodes))
	for _, e := range episodes {
		episodeIDs = append(episodeIDs, domain.EpisodeID(e.ID))
	}
	textsByID, terr := c.d.EpisodeTexts.ListByEpisodeIDsWithFallback(ctx, episodeIDs, lang)
	if terr != nil {
		// Failure of the i18n batch should NOT 5xx the composer —
		// degrades the season list to canon titles (mirrors the
		// per-row behaviour where each missing row was silently
		// swallowed).
		c.d.Logger.WarnContext(ctx, "episode_texts_batch_failed",
			slog.Int64("series_id", int64(d.SeriesID)),
			slog.String("lang", lang),
			slog.Int("episode_count", len(episodeIDs)),
			slog.String("error", terr.Error()))
		textsByID = nil
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
			if t, ok := textsByID[domain.EpisodeID(e.ID)]; ok {
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

// computeDegraded — single source of truth for the degraded[] list.
// Reads canon.enrichment_*_synced_at + enrichment_errors per source via
// the EnrichmentFreshnessPort, applies §5.6 rules via
// enrichment.Degraded, then ORs in the branch-failure flags from the
// tracker.
func (c *Composer) computeDegraded(ctx context.Context, seriesID domain.SeriesID, canon series.Canon, br *branchTracker) ([]enrichment.Source, error) {
	sources := []enrichment.Source{
		enrichment.SourceTMDBSeries,
		enrichment.SourceTMDBSeason,
		enrichment.SourceTMDBPerson,
		enrichment.SourceOMDb,
	}
	// Single read of every error row for the series — populate Errors map.
	errorBySource := map[enrichment.Source]*enrichment.EnrichmentError{}
	if c.d.Freshness != nil {
		errs, err := c.d.Freshness.ErrorsFor(ctx, seriesID)
		if err != nil {
			c.d.Logger.WarnContext(ctx, "enrichment_errors_lookup_failed",
				slog.String("domain", "enrichment"),
				slog.Int64("series_id", int64(seriesID)),
				slog.String("error", err.Error()))
		} else {
			now := c.d.Now()
			for i := range errs {
				e := errs[i]
				if !e.IsLive(48*time.Hour, now) {
					continue
				}
				errorBySource[e.Source] = &e
			}
		}
	}
	in := enrichment.DegradedInput{
		SyncedAt:        map[enrichment.Source]*time.Time{},
		Errors:          errorBySource,
		TTLs:            map[enrichment.Source]time.Duration{},
		SonarrReachable: !br.failed(enrichment.SourceSonarr),
		QbitReachable:   true, // qBit branch is placeholder; assume reachable.
	}
	kind := classifyKind(canon)
	for _, s := range sources {
		switch s {
		case enrichment.SourceTMDBSeries:
			in.SyncedAt[s] = canon.EnrichmentTMDBSyncedAt
		case enrichment.SourceOMDb:
			in.SyncedAt[s] = canon.EnrichmentOMDBSyncedAt
		default:
			// tmdb_season / tmdb_person — no canon column. Read via the
			// adapter; nil result means "no per-entity column tracked
			// yet" which Degraded treats as rule-1 (never synced) — the
			// desired behaviour until those columns land in a future
			// schema iteration.
			if c.d.Freshness != nil {
				t, err := c.d.Freshness.SyncedAtFor(ctx, seriesID, s)
				if err != nil {
					c.d.Logger.WarnContext(ctx, "freshness_lookup_failed",
						slog.String("domain", "enrichment"),
						slog.String("source", string(s)),
						slog.Int64("series_id", int64(seriesID)),
						slog.String("error", err.Error()))
					in.SyncedAt[s] = nil
				} else {
					in.SyncedAt[s] = t
				}
			} else {
				in.SyncedAt[s] = nil
			}
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

// primarySubtag returns the leading BCP-47 primary subtag, lowercased.
// "ru-RU" → "ru"; "en" → "en"; "zh-Hant-TW" → "zh"; "" → "". Story 541.
func primarySubtag(lang string) string {
	lang = strings.TrimSpace(strings.ToLower(lang))
	if lang == "" {
		return ""
	}
	if i := strings.IndexByte(lang, '-'); i > 0 {
		return lang[:i]
	}
	return lang
}

// shouldPreferCanon returns true when the SeriesTexts fallback returned a
// row in a language that doesn't match the request AND canon's original
// language matches the requested primary subtag. In that case mapHero
// should fall back to canon.Title (which is the original-language title
// the catalog stores) instead of the en-US series_texts row that
// GetWithFallback returns by default.
//
// Example (live bug for series 25551 — "Новичок" / "The Rookie"):
//
//	canon.OriginalLanguage = "ru"
//	requestedLang          = "ru-RU"
//	got.Language           = "en-US"
//	→ true → drop d.Text, let mapHero render canon.Title ("Новичок")
//
// Story 541.
func shouldPreferCanon(canon series.Canon, requestedLang, gotLang string) bool {
	if canon.OriginalLanguage == nil || *canon.OriginalLanguage == "" {
		return false
	}
	reqSub := primarySubtag(requestedLang)
	if reqSub == "" {
		return false
	}
	if primarySubtag(*canon.OriginalLanguage) != reqSub {
		return false
	}
	return primarySubtag(gotLang) != reqSub
}

func pickContentRating(rs []enrichpersistence.ContentRating, lang string) *enrichpersistence.ContentRating {
	// Pick by user-locale country prefix (lang="ru-RU" → "RU"),
	// then en-US ("US"), then first available. v1 keeps the
	// matching naive — composer correctness over locale subtlety.
	country := ""
	if i := strings.Index(lang, "-"); i > 0 {
		country = strings.ToUpper(lang[i+1:])
	}
	var pickUS *enrichpersistence.ContentRating
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

// GetCanonicalSeasons returns canon seasons + episodes for a series
// WITHOUT per-instance state (no EpisodeStates, no SeasonStats). Used
// by the TMDB-fallback path (Story 533a) when the series is not carried
// by any Sonarr instance. Episode text fallback follows the same
// EpisodeTextsPort.GetWithFallback chain as the per-instance branch.
//
// Result invariants:
//   - Returns an empty (non-nil) slice when the series has no seasons.
//   - Each SeasonDetail has Stats=nil (no per-instance projection).
//   - Each EpisodeDetail has State=nil (no per-instance state).
//   - EpisodeDetail.Text is populated when EpisodeTexts returns a row;
//     ErrNotFound is treated as "no localized row" and silently skipped.
func (c *Composer) GetCanonicalSeasons(ctx context.Context, seriesID domain.SeriesID, lang string) ([]SeasonDetail, error) {
	lang = resolveLang(lang)
	seasons, err := c.d.Seasons.ListBySeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list seasons: %w", err)
	}
	episodes, err := c.d.Episodes.ListBySeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list episodes: %w", err)
	}
	bySeason := make(map[int][]series.CanonEpisode)
	for _, e := range episodes {
		bySeason[e.SeasonNumber] = append(bySeason[e.SeasonNumber], e)
	}

	// Story 550 — batch fallback (mirrors loadSeasonsAndEpisodes).
	// EpisodeTexts is nil-OK on this fallback path (per port doc) —
	// when wired we batch; when nil we leave every EpisodeDetail.Text
	// blank (canon-only render).
	var textsByID map[domain.EpisodeID]series.EpisodeText
	if c.d.EpisodeTexts != nil {
		episodeIDs := make([]domain.EpisodeID, 0, len(episodes))
		for _, e := range episodes {
			episodeIDs = append(episodeIDs, domain.EpisodeID(e.ID))
		}
		var terr error
		textsByID, terr = c.d.EpisodeTexts.ListByEpisodeIDsWithFallback(ctx, episodeIDs, lang)
		if terr != nil {
			c.d.Logger.WarnContext(ctx, "canonical_episode_texts_batch_failed",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("lang", lang),
				slog.Int("episode_count", len(episodeIDs)),
				slog.String("err", terr.Error()))
			textsByID = nil
		}
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
			if t, ok := textsByID[domain.EpisodeID(e.ID)]; ok {
				et := t
				ed.Text = &et
			}
			epDetails = append(epDetails, ed)
		}
		out = append(out, SeasonDetail{Canon: s, Episodes: epDetails})
	}
	// Best-effort media resolve. Sync for season posters (above-the-fold),
	// async for episode stills (below-the-fold). Mirrors composer.go
	// resolveAssets shape so the wire is stable across instance + fallback.
	if c.d.MediaResolver != nil {
		syncCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		for i := range out {
			out[i].Canon.PosterAsset = c.d.MediaResolver.ResolveSync(syncCtx, out[i].Canon.PosterAsset, "w154", "season_poster_w154")
			for j := range out[i].Episodes {
				out[i].Episodes[j].Canon.StillAsset = c.d.MediaResolver.Resolve(ctx, out[i].Episodes[j].Canon.StillAsset, "w300", "still_w300")
			}
		}
		cancel()
	}
	c.d.Logger.InfoContext(ctx, "canonical_seasons_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("lang", lang),
		slog.Int("season_count", len(out)))
	return out, nil
}

// GetCanonicalCast returns top-N cast rows for a series with no
// in_library probe (the H-1 per-instance composer does that). Used by
// the TMDB-fallback path (Story 533a). Limit defaults to CastDefaultLimit.
//
// Result invariants:
//   - Returns an empty (non-nil) slice when there are no cast rows.
//   - Credits whose People row is missing are silently dropped (cast
//     list shrinks gracefully — matches loadTopCast).
//   - ProfileAsset is async-resolved through MediaResolver.
func (c *Composer) GetCanonicalCast(ctx context.Context, seriesID domain.SeriesID, limit int) ([]CastDetail, error) {
	if limit <= 0 {
		limit = CastDefaultLimit
	}
	credits, err := c.d.SeriesPeople.ListBySeries(ctx, seriesID, people.SeriesCreditCast)
	if err != nil {
		return nil, fmt.Errorf("list series_people: %w", err)
	}
	if len(credits) > limit {
		credits = credits[:limit]
	}
	if len(credits) == 0 {
		return []CastDetail{}, nil
	}
	ids := make([]int64, 0, len(credits))
	for _, cr := range credits {
		ids = append(ids, cr.PersonID)
	}
	persons, err := c.d.People.ListByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list people by ids: %w", err)
	}
	byID := make(map[int64]people.Person, len(persons))
	for _, p := range persons {
		byID[p.ID] = p
	}
	out := make([]CastDetail, 0, len(credits))
	for _, cr := range credits {
		p, ok := byID[cr.PersonID]
		if !ok {
			continue
		}
		out = append(out, CastDetail{Credit: cr, Person: p})
	}
	if c.d.MediaResolver != nil {
		for i := range out {
			out[i].Person.ProfileAsset = c.d.MediaResolver.Resolve(ctx, out[i].Person.ProfileAsset, "w185", "profile_w185")
		}
	}
	c.d.Logger.InfoContext(ctx, "canonical_cast_composed",
		slog.Int64("series_id", int64(seriesID)),
		slog.Int("cast_count", len(out)))
	return out, nil
}
