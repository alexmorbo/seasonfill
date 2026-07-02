package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// LibraryStaleThreshold — M-2 staleness window. When a series_cache row's
// updated_at is older than this, LibraryComposer fires a best-effort,
// non-blocking sonarr_sync refresh (via LibrarySyncTrigger) and returns the
// CURRENT projection. The read is never blocked on the refresh. 6h mirrors the
// sonarr_sync cron cadence (0 */6 * * *) so a stale read one full cron-miss late
// self-heals on the next open.
const LibraryStaleThreshold = 6 * time.Hour

// libraryRecentLimit caps the grab_records rows read for the recent-activity
// strip (PLAN §7.2 "LIMIT 5 recent").
const libraryRecentLimit = 5

// grabStatusImported is the grab.StatusImported literal. Duplicated here (not
// imported from the grab bounded context) so the seriesdetail app layer stays
// free of a cross-context dependency for one enum comparison.
const grabStatusImported = "imported"

// ErrSeriesNotInInstance is returned by LibraryComposer.Compose when the
// canonical series.id has no series_cache row on the requested instance (the
// instance is unknown to this series, or the row was removed between the
// handler's resolution and the compose read). The handler maps it to
// 404 instance_not_found.
var ErrSeriesNotInInstance = errors.New("series not in instance")

// GrabEvent is the projection LibraryComposer reads from grab_records for the
// recent-activity strip + last-grab / last-import stamps. Use-case-local so
// LibraryGrabHistoryPort stays free of the grab bounded context's wide Record
// type (mirrors the PersonCreditRef pattern on the composer).
type GrabEvent struct {
	Status       string
	SeasonNumber int
	ReleaseTitle string
	Quality      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// LibraryGrabHistoryPort lists the most-recent grab_records rows for one
// (instance, sonarr_series_id), newest-first, capped at limit. An empty slice
// (not an error) means the series has no grab history yet. Backed by
// GrabRepository.List via cmd/server/adapters.GrabHistoryAdapter.
type LibraryGrabHistoryPort interface {
	RecentBySeries(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID, limit int) ([]GrabEvent, error)
}

// LibrarySyncTrigger requests a best-effort, non-blocking sonarr_sync refresh
// for one (instance, sonarr_series_id). M-2: LibraryComposer fires this when
// series_cache is stale and returns the CURRENT projection without blocking.
// Implementations MUST return immediately (fire-and-forget). nil-OK: when the
// field is nil the composer skips the enqueue.
type LibrarySyncTrigger interface {
	TriggerSeriesSync(instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID)
}

// LibraryStripView mirrors dto.LibraryStrip's Sonarr-derived counters. Kept as
// an app-layer type so LibraryComposer never imports the HTTP DTO package (the
// handler maps LibraryStripView → dto.LibraryStrip, same as Composer.Get →
// *Detail → dto.SeriesDetailResponse).
type LibraryStripView struct {
	Monitored       bool
	EpisodesTotal   int
	EpisodesOnDisk  int
	EpisodesAired   int
	MissingCount    int
	SizeOnDiskBytes int64
	DominantQuality string
}

// LibrarySeasonCountView is LibraryComposer's per-season on-disk / downloading
// tally. App-local (the handler maps it → dto.LibrarySeasonCount) so the app
// layer never imports the HTTP DTO package (mirrors LibraryStripView).
type LibrarySeasonCountView struct {
	SeasonNumber   int
	EpisodesOnDisk int
	Downloading    int
}

// LibraryView is LibraryComposer's domain object — the handler maps it onto
// dto.SeriesLibraryResponse without further DB queries. Reuses the composer's
// InProgressDetail / NextEpisodeDetail / RecentItem so wire projections stay
// identical to the fat-composer path.
type LibraryView struct {
	Instance       domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
	SeriesID       domain.SeriesID
	Monitored      bool
	Strip          LibraryStripView
	Recent         []RecentItem
	InProgress     *InProgressDetail
	// Download is the hero download chip — the FIRST Sonarr /queue record (queue
	// order), mirroring the pre-B1b fat-response `download`. nil when the queue is
	// empty OR Sonarr is unreachable. Distinct from InProgress (the highest-percent
	// downloading pick for the in-progress pill). Reuses the SAME queueRecords slice
	// C3c-2 already loads — no extra Sonarr call. Story 971 / C3c-3.
	Download         *QueueRecordDetail
	NextEpisodeToAir *NextEpisodeDetail
	// SeasonCounts is the per-season on-disk / downloading breakdown for the
	// accordion row counters. Season-number ASC. Empty when the series has no
	// canon episodes. Story 970.
	SeasonCounts   []LibrarySeasonCountView
	LastGrabAt     *time.Time
	LastImportedAt *time.Time
	SyncedAt       time.Time
	// StaleEnqueued reports whether the M-2 staleness branch fired a
	// sonarr_sync trigger this call. Surfaced for observability + tests; the
	// handler does not project it onto the wire.
	StaleEnqueued bool
}

// LibraryDeps groups LibraryComposer's narrow ports. Now/Logger are nil-OK.
type LibraryDeps struct {
	CacheLookup   SeriesCacheLookupPort
	Episodes      EpisodesPort
	EpisodeStates EpisodeStatesPort
	// SeasonStats is the per-(instance, series, season) Sonarr statistics
	// projection (season_stats). Story 974: per-season on-disk counts read
	// EpisodeFileCount from here — the authoritative Sonarr counter refreshed
	// every scan by fillSeriesCache — instead of the episode_states.HasFile
	// walk, which the cron scan never updates. nil-OK: when nil (or on a
	// per-season miss) buildSeasonCounts falls back to the episode_states walk.
	SeasonStats SeasonStatsPort
	GrabHistory LibraryGrabHistoryPort
	SonarrFor   func(instanceName domain.InstanceName) (SonarrQueueLister, bool)
	SyncTrigger LibrarySyncTrigger
	Logger      *slog.Logger
	Now         func() time.Time
}

// LibraryComposer is the E-1-B2 use case: it projects per-instance Sonarr
// library state for one canonical series onto a LibraryView. Read-through DB
// projection + one optional live Sonarr /queue call. No TMDB, no LRU.
type LibraryComposer struct {
	d LibraryDeps
}

// NewLibraryComposer constructs the use case. Logger defaults to slog.Default,
// Now defaults to time.Now().UTC.
func NewLibraryComposer(d LibraryDeps) *LibraryComposer {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &LibraryComposer{d: d}
}

// Compose resolves the (seriesID, instanceName) series_cache row and projects
// the Sonarr library state. Returns ErrSeriesNotInInstance when the instance
// does not carry the series (handler → 404 instance_not_found).
func (lc *LibraryComposer) Compose(ctx context.Context, seriesID domain.SeriesID, instanceName domain.InstanceName) (LibraryView, error) {
	entries, err := lc.d.CacheLookup.ListBySeriesID(ctx, seriesID)
	if err != nil {
		return LibraryView{}, fmt.Errorf("library compose: list cache: %w", err)
	}
	entry, found := selectInstanceEntry(entries, instanceName)
	if !found {
		return LibraryView{}, fmt.Errorf("library compose %s/%d: %w", instanceName, int64(seriesID), ErrSeriesNotInInstance)
	}
	sonarrID := entry.SonarrSeriesID

	// M-2 — non-blocking staleness enqueue. Never blocks the read.
	staleEnqueued := lc.maybeEnqueueSync(entry)

	episodes, err := lc.d.Episodes.ListBySeries(ctx, seriesID)
	if err != nil {
		return LibraryView{}, fmt.Errorf("library compose: episodes: %w", err)
	}
	states, err := lc.d.EpisodeStates.ListBySeries(ctx, instanceName, seriesID)
	if err != nil {
		return LibraryView{}, fmt.Errorf("library compose: episode states: %w", err)
	}

	grabEvents := lc.loadRecent(ctx, instanceName, sonarrID)
	queueRecords := lc.loadQueueRecords(ctx, instanceName, sonarrID)
	seasonStats := lc.loadSeasonStats(ctx, instanceName, sonarrID)
	inProgress := pickInProgress(&Detail{QueueRecords: queueRecords})

	monitoredByEpisode := make(map[domain.EpisodeID]bool, len(states))
	for _, st := range states {
		monitoredByEpisode[st.EpisodeID] = st.Monitored
	}

	view := LibraryView{
		Instance:         instanceName,
		SonarrSeriesID:   sonarrID,
		SeriesID:         seriesID,
		Monitored:        entry.Monitored,
		Strip:            buildLibraryStrip(entry, len(episodes), states),
		Recent:           toRecentItems(grabEvents),
		InProgress:       inProgress,
		Download:         pickDownloadRecord(queueRecords),
		NextEpisodeToAir: pickNextToAir(episodes, monitoredByEpisode, lc.d.Now()),
		SeasonCounts:     buildSeasonCounts(episodes, states, seasonStats, queueRecords),
		LastGrabAt:       firstGrabAt(grabEvents),
		LastImportedAt:   firstImportedAt(grabEvents),
		SyncedAt:         maxTime(entry.UpdatedAt, latestStateUpdate(states)),
		StaleEnqueued:    staleEnqueued,
	}
	return view, nil
}

// selectInstanceEntry returns the cache row for the requested instance.
func selectInstanceEntry(entries []series.CacheEntry, instanceName domain.InstanceName) (series.CacheEntry, bool) {
	for _, e := range entries {
		if e.InstanceName == instanceName {
			return e, true
		}
	}
	return series.CacheEntry{}, false
}

// maybeEnqueueSync fires the M-2 best-effort sonarr_sync trigger when the cache
// row is stale. Returns true when a trigger was dispatched. nil SyncTrigger →
// no-op. The trigger implementation is fire-and-forget; this method never
// blocks. scan.UseCase already coalesces concurrent per-instance runs, so
// repeated stale reads during the refresh window do not stack scans.
func (lc *LibraryComposer) maybeEnqueueSync(entry series.CacheEntry) bool {
	if lc.d.SyncTrigger == nil {
		return false
	}
	if lc.d.Now().Sub(entry.UpdatedAt) <= LibraryStaleThreshold {
		return false
	}
	lc.d.SyncTrigger.TriggerSeriesSync(entry.InstanceName, entry.SonarrSeriesID)
	lc.d.Logger.Debug("library_stale_sync_enqueued",
		slog.String("instance", string(entry.InstanceName)),
		slog.Int("sonarr_series_id", int(entry.SonarrSeriesID)),
		slog.Time("cache_updated_at", entry.UpdatedAt))
	return true
}

// loadRecent reads the last-N grab_records. Degrade-tolerant: nil GrabHistory or
// a read error yields an empty strip (never fails the whole compose).
func (lc *LibraryComposer) loadRecent(ctx context.Context, instanceName domain.InstanceName, sonarrID domain.SonarrSeriesID) []GrabEvent {
	if lc.d.GrabHistory == nil {
		return nil
	}
	events, err := lc.d.GrabHistory.RecentBySeries(ctx, instanceName, sonarrID, libraryRecentLimit)
	if err != nil {
		lc.d.Logger.WarnContext(ctx, "library_recent_grabs_failed",
			slog.String("instance", string(instanceName)),
			slog.Int("sonarr_series_id", int(sonarrID)),
			slog.String("error", err.Error()))
		return nil
	}
	return events
}

// loadQueueRecords reads the live Sonarr /queue and returns ALL in-flight
// records for the series. Degrade-tolerant: nil SonarrFor / unreachable Sonarr /
// query error yields nil (no error) — the in-progress pill is absent and
// per-season downloading counts fall back to 0. The single fetched slice feeds
// both pickInProgress and buildSeasonCounts (no double queue call).
func (lc *LibraryComposer) loadQueueRecords(ctx context.Context, instanceName domain.InstanceName, sonarrID domain.SonarrSeriesID) []QueueRecordDetail {
	if lc.d.SonarrFor == nil {
		return nil
	}
	lister, ok := lc.d.SonarrFor(instanceName)
	if !ok || lister == nil {
		return nil
	}
	q, err := lister.Queue(ctx, sonarrID)
	if err != nil {
		lc.d.Logger.WarnContext(ctx, "library_sonarr_queue_failed",
			slog.String("instance", string(instanceName)),
			slog.Int("sonarr_series_id", int(sonarrID)),
			slog.String("error", err.Error()))
		return nil
	}
	recs := make([]QueueRecordDetail, 0, len(q.Records))
	for _, rec := range q.Records {
		recs = append(recs, QueueRecordDetail{
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
	return recs
}

// loadSeasonStats reads the per-season Sonarr statistics projection
// (season_stats) for the instance and returns a season_number → SeasonStat
// map. Degrade-tolerant: a nil SeasonStats port or a read error yields nil, so
// buildSeasonCounts falls back to the episode_states walk per season. Mirrors
// composer.go's story-377 season_stats load semantics (warn-log, never fail the
// compose). No extra Sonarr call — season_stats is a DB read refreshed by the
// scan loop's fillSeriesCache.
func (lc *LibraryComposer) loadSeasonStats(ctx context.Context, instanceName domain.InstanceName, sonarrID domain.SonarrSeriesID) map[int]series.SeasonStat {
	if lc.d.SeasonStats == nil {
		return nil
	}
	stats, err := lc.d.SeasonStats.ListBySeries(ctx, instanceName, sonarrID)
	if err != nil {
		lc.d.Logger.WarnContext(ctx, "library_season_stats_failed",
			slog.String("instance", string(instanceName)),
			slog.Int("sonarr_series_id", int(sonarrID)),
			slog.String("error", err.Error()))
		return nil
	}
	out := make(map[int]series.SeasonStat, len(stats))
	for _, st := range stats {
		out[st.SeasonNumber] = st
	}
	return out
}

// pickDownloadRecord restores the pre-B1b hero download chip: the FIRST record in
// the Sonarr /queue response for the series (Sonarr queue order, preserved by
// loadQueueRecords). Mirrors the old composer's unconditional `d.Queue =
// &d.QueueRecords[0]` pick — deliberately NOT the status=="downloading" /
// highest-percent filter used by pickInProgress; the two were distinct selections
// in the fat response and the hero chip surfaced this raw-first record. Returns
// nil when the queue is empty (Sonarr returned no records OR was unreachable →
// loadQueueRecords yields nil), so the hero renders no chip. No extra Sonarr call:
// operates on the already-loaded slice. Story 971 / C3c-3.
func pickDownloadRecord(records []QueueRecordDetail) *QueueRecordDetail {
	if len(records) == 0 {
		return nil
	}
	first := records[0]
	return &first
}

// buildSeasonCounts tallies per-season on-disk + downloading for one instance.
//
// On-disk source (bug #974): per-season EpisodeFileCount from season_stats —
// the authoritative Sonarr seasons[].statistics projection refreshed on EVERY
// scan by fillSeriesCache. This mirrors the SeasonsAccordion header (story 377,
// series_detail.go mapSeasons) and the library-strip aggregate
// (series_cache.EpisodeFileCount), so the collapsed per-season counter, the
// expanded single-season on_disk_count, and the hero strip all agree.
//
// episode_states.HasFile is NOT authoritative here: it is written only by the
// webhook SeriesAdd full-sync path (syncEpisodes), never by the cron scan, so a
// series whose files landed AFTER the initial add keeps HasFile=false while
// series_cache/season_stats are refreshed every scan — the exact bug #974. It
// is kept ONLY as a per-season fallback for seasons with no season_stats row
// yet (e.g. a brand-new series between add and first scan), matching
// mapSeasons's nil-Stats fallback.
//
// Downloading = live Sonarr queue records with status=="downloading" per season
// (best-effort; all zero when Sonarr is unreachable). Specials (season 0) are
// included when present. Every season with a canon episode gets a row even at 0
// on disk so the FE can render "0/total". Returns a season-number-ASC slice;
// empty when the series has no canon episodes.
func buildSeasonCounts(
	episodes []series.CanonEpisode,
	states []series.EpisodeState,
	statsBySeason map[int]series.SeasonStat,
	queue []QueueRecordDetail,
) []LibrarySeasonCountView {
	if len(episodes) == 0 {
		return nil
	}
	seasonByEpisode := make(map[domain.EpisodeID]int, len(episodes))
	seasons := make(map[int]*LibrarySeasonCountView)
	ensure := func(n int) *LibrarySeasonCountView {
		sc, ok := seasons[n]
		if !ok {
			sc = &LibrarySeasonCountView{SeasonNumber: n}
			seasons[n] = sc
		}
		return sc
	}
	for _, ep := range episodes {
		seasonByEpisode[domain.EpisodeID(ep.ID)] = ep.SeasonNumber
		ensure(ep.SeasonNumber) // season present even with 0 on disk
	}

	// Fallback tally: episode_states.HasFile per season, consulted only for a
	// season that has no season_stats row (see docstring). A state row for an
	// episode not in canon is skipped.
	fallbackOnDisk := make(map[int]int, len(seasons))
	for _, st := range states {
		if !st.HasFile {
			continue
		}
		n, ok := seasonByEpisode[st.EpisodeID]
		if !ok {
			continue
		}
		fallbackOnDisk[n]++
	}

	for n, sc := range seasons {
		if stat, ok := statsBySeason[n]; ok {
			sc.EpisodesOnDisk = stat.EpisodeFileCount
		} else {
			sc.EpisodesOnDisk = fallbackOnDisk[n]
		}
	}

	for _, rec := range queue {
		if rec.Status != "downloading" {
			continue
		}
		if _, ok := seasons[rec.SeasonNumber]; !ok {
			continue // queue record for a season with no canon episodes — skip
		}
		seasons[rec.SeasonNumber].Downloading++
	}
	out := make([]LibrarySeasonCountView, 0, len(seasons))
	for _, sc := range seasons {
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SeasonNumber < out[j].SeasonNumber })
	return out
}

// buildLibraryStrip mirrors rest.mapLibrary: on-disk / aired / size / missing
// come straight from the authoritative series_cache counters; total episodes =
// len(canon episodes); dominant quality = mode over on-disk episode_states.
func buildLibraryStrip(entry series.CacheEntry, totalEpisodes int, states []series.EpisodeState) LibraryStripView {
	return LibraryStripView{
		Monitored:       entry.Monitored,
		EpisodesTotal:   totalEpisodes,
		EpisodesOnDisk:  entry.EpisodeFileCount,
		EpisodesAired:   entry.AiredEpisodeCount,
		MissingCount:    entry.MissingCount,
		SizeOnDiskBytes: entry.SizeOnDiskBytes,
		DominantQuality: dominantQuality(states),
	}
}

// dominantQuality returns the most common quality string across on-disk
// episode_states. Ties broken by quality string ASC so the pick is
// deterministic (map iteration order is not). Empty when nothing on disk.
func dominantQuality(states []series.EpisodeState) string {
	counts := map[string]int{}
	for _, st := range states {
		if !st.HasFile || st.Quality == nil || *st.Quality == "" {
			continue
		}
		counts[*st.Quality]++
	}
	best := ""
	bestN := 0
	for q, n := range counts {
		if n > bestN || (n == bestN && q < best) {
			best = q
			bestN = n
		}
	}
	return best
}

// toRecentItems projects grab events onto the recent-activity strip.
func toRecentItems(events []GrabEvent) []RecentItem {
	out := make([]RecentItem, 0, len(events))
	for _, ev := range events {
		out = append(out, RecentItem{
			EventType: ev.Status,
			Subject:   recentSubject(ev),
			At:        ev.CreatedAt,
		})
	}
	return out
}

// recentSubject renders a one-line human description for a grab event.
func recentSubject(ev GrabEvent) string {
	label := "series"
	if ev.SeasonNumber > 0 {
		label = fmt.Sprintf("S%02d", ev.SeasonNumber)
	}
	if ev.ReleaseTitle != "" {
		return label + " · " + ev.ReleaseTitle
	}
	return label
}

// firstGrabAt returns the newest grab's created_at (events are newest-first).
func firstGrabAt(events []GrabEvent) *time.Time {
	if len(events) == 0 {
		return nil
	}
	t := events[0].CreatedAt
	return &t
}

// firstImportedAt returns the updated_at of the newest imported grab.
func firstImportedAt(events []GrabEvent) *time.Time {
	for _, ev := range events {
		if ev.Status == grabStatusImported {
			t := ev.UpdatedAt
			return &t
		}
	}
	return nil
}

// pickNextToAir returns the earliest future-dated non-Specials episode,
// preferring monitored episodes (matches composer.pickNextEpisode semantics).
// Title stays nil — titles live in episode_texts (TMDB canon context), out of
// scope for this Sonarr-state endpoint.
func pickNextToAir(episodes []series.CanonEpisode, monitored map[domain.EpisodeID]bool, now time.Time) *NextEpisodeDetail {
	var bestMonitored, bestAny *NextEpisodeDetail
	for _, ep := range episodes {
		if ep.SeasonNumber <= 0 {
			continue
		}
		if ep.AirDate == nil || !ep.AirDate.After(now) {
			continue
		}
		cand := &NextEpisodeDetail{
			SeasonNumber:  ep.SeasonNumber,
			EpisodeNumber: ep.EpisodeNumber,
			AirDate:       ep.AirDate,
		}
		if bestAny == nil || isEarlier(cand, bestAny) {
			bestAny = cand
		}
		if monitored[domain.EpisodeID(ep.ID)] {
			if bestMonitored == nil || isEarlier(cand, bestMonitored) {
				bestMonitored = cand
			}
		}
	}
	if bestMonitored != nil {
		return bestMonitored
	}
	return bestAny
}

// latestStateUpdate returns the newest episode_states.updated_at (zero when
// there are no states) — the episode_states side of SyncedAt.
func latestStateUpdate(states []series.EpisodeState) time.Time {
	var newest time.Time
	for _, st := range states {
		if st.UpdatedAt.After(newest) {
			newest = st.UpdatedAt
		}
	}
	return newest
}

// maxTime returns the later of two timestamps.
func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
