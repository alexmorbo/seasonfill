package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SeriesWorkerDeps is the dependency surface — kept verbose
// (every repo is a named field, not a generic map) so a missing
// dependency surfaces as a nil-deref in the constructor's
// validate step, NOT inside the hot path under load.
type SeriesWorkerDeps struct {
	TMDB            TMDBClient
	Tx              Transactor
	Language        string // "en-US" — only language Story 211 writes; ru lands in C-5
	Series          SeriesRepo
	SeriesTexts     SeriesTextsRepo
	Seasons         SeasonsRepo
	Episodes        EpisodesRepo
	EpisodeTexts    EpisodeTextsRepo
	People          PeopleRepo
	SeriesPeople    SeriesPeopleRepo
	Genres          GenresRepo
	Keywords        KeywordsRepo
	Networks        NetworksRepo
	Companies       CompaniesRepo
	Videos          VideosRepoPort
	ContentRatings  ContentRatingsRepoPort
	ExternalIDs     ExternalIDsRepoPort
	Recommendations RecommendationsRepoPort
	SyncLog         SyncLogRepo
	MediaPrewarmer  MediaPrewarmer // nil OK — F-1 not yet shipped
	// Dispatcher (212): post-tx enqueue seam for the person worker.
	// nil OK — keeps the existing test fixtures green; production
	// wiring passes the shared *DispatcherImpl.
	Dispatcher Dispatcher
	Logger     *slog.Logger
	Clock      func() time.Time // injected for tests; defaults to time.Now
}

// SeriesWorker is the bound worker. Construct via NewSeriesWorker.
type SeriesWorker struct {
	deps SeriesWorkerDeps
}

// NewSeriesWorker validates that every required dependency is
// non-nil and returns the worker. Logger defaults to slog.Default;
// Clock defaults to time.Now; Language defaults to "en-US".
func NewSeriesWorker(deps SeriesWorkerDeps) (*SeriesWorker, error) {
	if deps.TMDB == nil {
		return nil, errors.New("enrichment.series_worker: TMDB client required")
	}
	if deps.Tx == nil {
		return nil, errors.New("enrichment.series_worker: Transactor required")
	}
	if deps.Series == nil || deps.SeriesTexts == nil || deps.Seasons == nil ||
		deps.Episodes == nil || deps.EpisodeTexts == nil ||
		deps.People == nil || deps.SeriesPeople == nil ||
		deps.Genres == nil || deps.Keywords == nil ||
		deps.Networks == nil || deps.Companies == nil ||
		deps.Videos == nil || deps.ContentRatings == nil ||
		deps.ExternalIDs == nil || deps.Recommendations == nil ||
		deps.SyncLog == nil {
		return nil, errors.New("enrichment.series_worker: every repository port is required")
	}
	if deps.Language == "" {
		deps.Language = "en-US"
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &SeriesWorker{deps: deps}, nil
}

// Handle is the dispatcher-facing entry point. seriesID is a CANON
// series.id (NOT series_cache.id). Returns an error only on a
// terminal failure that should NOT bubble (the worker journals
// outcome=error / not_found internally before returning).
func (w *SeriesWorker) Handle(ctx context.Context, seriesID domain.SeriesID) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceTMDBSeries)),
	)

	// 1. Read current canon — we need the tmdb_id to call TMDB AND
	//    the existing hydration level so MergeSeries lifts stub→full
	//    on success.
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.handle.series_missing",
				slog.Int64("series_id", int64(seriesNF.ID)),
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("series worker: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		// No tmdb_id — TMDB cannot enrich. Journal terminal not_found.
		w.journalNotFound(ctx, seriesID, "no tmdb_id on canon", start)
		return nil
	}

	// 2. Staleness short-circuit: ok + IsStale=false ⇒ skip.
	last, err := w.deps.SyncLog.GetLastSync(ctx, enrichment.EntityTypeSeries, int64(seriesID), enrichment.SourceTMDBSeries)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		log.WarnContext(ctx, "enrichment.series.handle.sync_log_read_failed",
			slog.String("error", err.Error()))
	}
	if last.Outcome == enrichment.OutcomeOK && last.SyncedAt != nil {
		ttl := enrichment.TTL(enrichment.SourceTMDBSeries, classifyKind(canon))
		if ttl > 0 && w.deps.Clock().Sub(*last.SyncedAt) < ttl {
			log.DebugContext(ctx, "enrichment.series.handle.fresh_skip",
				slog.String("outcome", string(last.Outcome)),
				slog.Time("synced_at", *last.SyncedAt),
			)
			return nil
		}
	}

	// 3. Fetch TV payload + active seasons.
	tv, err := w.deps.TMDB.GetTV(ctx, int64(*canon.TMDBID), w.deps.Language)
	if err != nil {
		return w.handleTMDBError(ctx, seriesID, "GetTV", err, last.Attempts, start)
	}

	// 4. Fetch each active season — PRD §5.5 "первый проход качает
	//    все сезоны". On first hydration we hit every season; on
	//    subsequent refreshes only "active" seasons. The classifier
	//    runs on the freshly-fetched TV row.
	seasonResponses := make(map[int]*tmdb.SeasonResponse, len(tv.Seasons))
	for _, s := range tv.Seasons {
		if !seasonNeedsFetch(s, last) {
			continue
		}
		sr, err := w.deps.TMDB.GetSeason(ctx, int64(*canon.TMDBID), s.SeasonNumber, w.deps.Language)
		if err != nil {
			return w.handleTMDBError(ctx, seriesID, fmt.Sprintf("GetSeason(%d)", s.SeasonNumber), err, last.Attempts, start)
		}
		seasonResponses[s.SeasonNumber] = sr
	}

	// 5. Map every payload OUTSIDE the tx — no DB I/O above this
	//    line means a TMDB 5xx leaves zero half-written rows.
	mapped := w.mapAll(tv, seasonResponses, canon)

	// 6. ONE tx for the whole graph.
	var enqueueRows []personEnqueueRow
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		rows, err := w.applyAll(txCtx, canon, tv, mapped, log)
		if err != nil {
			return err
		}
		enqueueRows = rows
		return nil
	})
	if err != nil {
		return w.handleTMDBError(ctx, seriesID, "tx", err, last.Attempts, start)
	}

	// 7. Journal success.
	now := w.deps.Clock()
	dur := int(now.Sub(start).Milliseconds())
	w.journalOK(ctx, seriesID, now, dur)

	// 8. Media pre-warm — nil-OK enqueue.
	if w.deps.MediaPrewarmer != nil {
		w.deps.MediaPrewarmer.Enqueue(ctx, mapped.PrewarmAssets)
	}

	// 9. 212: enqueue persons post-tx (NOT inside the tx — calling
	//    Dispatcher.Enqueue inside a tx is a layering violation).
	//    Hot/cold split based on credit_order; the dispatcher's dedup
	//    handles double-enqueues for the same person.
	if w.deps.Dispatcher != nil {
		w.enqueuePersons(ctx, enqueueRows, log)
	}

	log.InfoContext(ctx, "enrichment.series.handle.ok",
		slog.String("outcome", string(enrichment.OutcomeOK)),
		slog.Int("seasons_fetched", len(seasonResponses)),
		slog.Int("persons_enqueued", len(enqueueRows)),
		slog.Int("duration_ms", dur),
	)
	return nil
}

// personEnqueueRow pairs a resolved canon person.id with the TMDB
// billing position from the cast row. CreditOrder=999 is the crew
// sentinel — crew has no billing order, so it ALWAYS lands in the
// cold bucket.
type personEnqueueRow struct {
	PersonID    int64
	CreditOrder int
}

// enqueuePersons fans rows out to the dispatcher with hot/cold split.
// credit_order < 10 → hot; ≥10 (including crew sentinel 999) → cold.
// The dispatcher's dedup handles double-enqueues for the same person.
func (w *SeriesWorker) enqueuePersons(ctx context.Context, rows []personEnqueueRow, log *slog.Logger) {
	_ = ctx
	const topN = 10
	hot, cold := 0, 0
	for _, r := range rows {
		p := PriorityCold
		if r.CreditOrder < topN {
			p = PriorityHot
			hot++
		} else {
			cold++
		}
		w.deps.Dispatcher.Enqueue(EntityPerson, r.PersonID, p)
	}
	log.Debug("enrichment.series.handle.persons_enqueued",
		slog.Int("hot", hot),
		slog.Int("cold", cold),
	)
}

// classifyKind picks the TTL bucket from the canon's Status /
// InProduction fields. "Continuing" / "Returning Series" / in
// production → KindSeriesContinuing; otherwise → KindSeriesEnded.
// (PRD §5.5 TTL matrix.)
func classifyKind(c series.Canon) enrichment.Kind {
	if c.InProduction {
		return enrichment.KindSeriesContinuing
	}
	if c.Status == nil {
		return enrichment.KindSeriesEnded
	}
	switch *c.Status {
	case "Returning Series", "In Production", "Pilot", "Planned":
		return enrichment.KindSeriesContinuing
	}
	return enrichment.KindSeriesEnded
}

// seasonNeedsFetch — first hydration (last is zero) ⇒ fetch ALL
// seasons; refresh ⇒ fetch ONLY active seasons (latest season +
// any with unaired episodes). Story 211 ships the simple heuristic
// "fetch all on first sync, fetch latest on refresh"; finer
// granularity lands when we have per-season sync_log rows (D-? in
// the PRD roadmap).
func seasonNeedsFetch(s tmdb.TVSeasonStub, last enrichment.SyncLog) bool {
	if last.Outcome != enrichment.OutcomeOK {
		return true
	}
	// On refresh: skip season 0 (specials, mostly static) and
	// closed seasons. Heuristic: season with no AirDate OR AirDate
	// > 365d ago AND we already have it ⇒ skip.
	if s.SeasonNumber == 0 {
		return false
	}
	if s.AirDate == "" {
		return true
	}
	t, err := time.Parse("2006-01-02", s.AirDate)
	if err != nil {
		return true
	}
	return time.Since(t) < 365*24*time.Hour
}

// mappedPayload bundles every value the worker needs inside the tx.
// Built in mapAll — pure-CPU; no I/O.
type mappedPayload struct {
	SeriesPatch     enrichment.SeriesPatch
	SeriesText      series.SeriesText
	Seasons         []series.CanonSeason
	Episodes        []series.CanonEpisode // canonical, fully-merged
	EpisodeTexts    []series.EpisodeText
	PersonStubs     []people.Person       // unique stubs from TV credits
	SeriesCredits   []people.SeriesCredit // SeriesID set; PersonID filled inside tx
	Genres          []taxonomy.Genre
	Keywords        []taxonomy.Keyword
	Networks        []taxonomy.Network
	Companies       []taxonomy.ProductionCompany
	Videos          []VideoRow
	ContentRatings  []tmdb.MappedContentRating
	ExternalIDs     []tmdb.MappedExternalID
	Recommendations []series.Canon // hydration=stub
	// PrewarmAssets is the per-series media-prewarm payload built by
	// mapAll. The series_worker hands the slice to MediaPrewarmer
	// AFTER the tx commits.
	PrewarmAssets []MediaPrewarmRequest
}

// mapAll runs every mapper from infrastructure/tmdb against the
// payload + composes the SeriesPatch + collects pre-warm asset
// list. Pure — no logger, no errors (the mappers are fallible
// over JSON parse, but every mapper here is total).
func (w *SeriesWorker) mapAll(tv *tmdb.TVResponse, seasons map[int]*tmdb.SeasonResponse, canon series.Canon) mappedPayload {
	out := mappedPayload{}

	tmdbCanon := tmdb.MapTVToCanon(tv)
	out.SeriesPatch = patchFromTMDBCanon(tmdbCanon)
	out.SeriesText = series.SeriesText{
		SeriesID: canon.ID, Language: w.deps.Language,
		Title: nonEmptyStringPtr(tv.Name), Overview: nonEmptyStringPtr(tv.Overview), Tagline: nonEmptyStringPtr(tv.Tagline),
	}

	out.Seasons = tmdb.MapTVToSeasons(tv)
	for i := range out.Seasons {
		out.Seasons[i].SeriesID = canon.ID
	}

	credits, stubs := tmdb.MapTVToCredits(tv)
	for i := range credits {
		credits[i].SeriesID = canon.ID
	}
	out.SeriesCredits = credits
	out.PersonStubs = stubs

	g, k, n, c := tmdb.MapTVToTaxonomy(tv)
	out.Genres, out.Keywords, out.Networks, out.Companies = g, k, n, c

	for _, v := range tmdb.MapTVToVideos(tv) {
		out.Videos = append(out.Videos, VideoRow{
			SeriesID: canon.ID, TMDBID: v.TMDBID,
			Language: v.Language, Country: v.Country,
			Name: v.Name, Key: v.Key, Site: v.Site,
			Type: v.Type, Official: v.Official,
			PublishedAt: v.PublishedAt, Size: v.Size,
		})
	}
	out.ContentRatings = tmdb.MapTVToContentRatings(tv)
	out.ExternalIDs = tmdb.MapTVToExternalIDs(tv)
	out.Recommendations = tmdb.MapTVToRecommendations(tv)

	// Episodes: walk every fetched season + accumulate canon
	// episodes + episode_texts. The tx-time resolver wires the
	// fresh season_id onto each episode.
	for _, sr := range seasons {
		seasonEps := tmdb.MapSeasonToEpisodes(sr, canon.ID, 0 /* resolved in tx */)
		for _, e := range seasonEps {
			out.Episodes = append(out.Episodes, e)
			if sr != nil {
				// One episode_text per episode (en-US — Story 211
				// language). Episode IDs are resolved post-batch
				// upsert; the text rows use a sentinel zero
				// EpisodeID that applyAll patches.
				out.EpisodeTexts = append(out.EpisodeTexts, series.EpisodeText{
					Language: w.deps.Language,
					Title:    nonEmptyStringPtr(findEpisodeTitle(sr, e.SeasonNumber, e.EpisodeNumber)),
					Overview: nonEmptyStringPtr(findEpisodeOverview(sr, e.SeasonNumber, e.EpisodeNumber)),
				})
			}
		}
	}

	// Pre-warm payload — PRD §6.4. Build full image URLs from the
	// raw TMDB paths the mapper persisted on canon entities. Order:
	// poster (w342 grid + w780 hero), backdrop w1280, network logos
	// w185, top-10 cast profiles w185, season posters w154, one
	// trailer thumbnail (best-effort YouTube hqdefault).
	out.PrewarmAssets = composePrewarmAssets(canon, out, tv)
	return out
}

// applyAll runs every repo write inside ONE tx. Order is the
// deterministic dependency-sorted sequence from PRD §5.6:
// canon → texts → seasons → episodes → episode_texts →
// people → series_people → taxonomy → videos → content_ratings →
// external_ids → recommendations.
//
// Returns the resolved person enqueue rows so the caller (Handle)
// can fan them out to the dispatcher AFTER the tx commits (calling
// Dispatcher.Enqueue inside a tx is a layering violation — the
// dispatcher's dedup window opens at enqueue time, not at commit).
func (w *SeriesWorker) applyAll(txCtx context.Context, canon series.Canon, tv *tmdb.TVResponse, m mappedPayload, log *slog.Logger) ([]personEnqueueRow, error) {
	// 1. Merge + upsert series canon.
	merged := enrichment.MergeSeries(
		canonToEnrichmentCanon(canon),
		m.SeriesPatch,
		enrichment.SourceTMDBSeries,
	)
	canonOut := enrichmentCanonToCanon(merged, canon)

	// 346: defensive write-side guard. Working hypothesis on the
	// backdrop NULL backlog is that the merge policy zeros the asset
	// before persist. The guard runs on the value about to be written
	// — if TMDB returned a non-empty path but canonOut still carries
	// nil/empty, WARN + force the path on so prod converges. Strictly
	// additive: only writes when both (a) TMDB has a path and (b)
	// canon-side is nil/empty, so a stored canon value is never
	// clobbered. Mirror behavior for poster.
	if tv != nil && tv.BackdropPath != "" && (canonOut.BackdropAsset == nil || *canonOut.BackdropAsset == "") {
		log.WarnContext(txCtx, "enrichment.series.canon.backdrop_write_gap",
			slog.Int64("series_id", int64(canon.ID)),
			slog.Any("tmdb_id", canonOut.TMDBID),
			slog.String("tmdb_backdrop_path", tv.BackdropPath),
			slog.String("reason", "merge_policy_zeroed_nonempty_path"),
		)
		bp := tv.BackdropPath
		canonOut.BackdropAsset = &bp
	}
	if tv != nil && tv.PosterPath != "" && (canonOut.PosterAsset == nil || *canonOut.PosterAsset == "") {
		log.WarnContext(txCtx, "enrichment.series.canon.poster_write_gap",
			slog.Int64("series_id", int64(canon.ID)),
			slog.Any("tmdb_id", canonOut.TMDBID),
			slog.String("tmdb_poster_path", tv.PosterPath),
			slog.String("reason", "merge_policy_zeroed_nonempty_path"),
		)
		pp := tv.PosterPath
		canonOut.PosterAsset = &pp
	}

	seriesID, err := w.deps.Series.Upsert(txCtx, canonOut)
	if err != nil {
		return nil, fmt.Errorf("upsert series canon: %w", err)
	}

	// 346: diagnostic — pinpoint backdrop write-gap. Audit found 100%
	// of recent canon rows NULL backdrop_asset; this log line
	// attributes blame to either (a) upstream TMDB sent no
	// backdrop_path, or (b) mapper/merge zeroed it before persist.
	// Sampled at INFO so it surfaces in the default log level on prod.
	tvHasPoster := tv != nil && tv.PosterPath != ""
	tvHasBackdrop := tv != nil && tv.BackdropPath != ""
	log.InfoContext(txCtx, "enrichment.series.canon.images_persisted",
		slog.Int64("series_id", int64(seriesID)),
		slog.Any("tmdb_id", canonOut.TMDBID),
		slog.Bool("poster_present", canonOut.PosterAsset != nil && *canonOut.PosterAsset != ""),
		slog.Bool("backdrop_present", canonOut.BackdropAsset != nil && *canonOut.BackdropAsset != ""),
		slog.Bool("tmdb_poster_path_present", tvHasPoster),
		slog.Bool("tmdb_backdrop_path_present", tvHasBackdrop),
	)

	// 2. series_texts.
	m.SeriesText.SeriesID = seriesID
	if err := w.deps.SeriesTexts.Upsert(txCtx, m.SeriesText); err != nil {
		return nil, fmt.Errorf("upsert series_texts: %w", err)
	}

	// 3. Seasons — upsert each + collect (number → season_id) for
	//    episode wiring.
	seasonIDByNumber := make(map[int]int64, len(m.Seasons))
	for _, s := range m.Seasons {
		s.SeriesID = seriesID
		id, err := w.deps.Seasons.Upsert(txCtx, s)
		if err != nil {
			return nil, fmt.Errorf("upsert season %d: %w", s.SeasonNumber, err)
		}
		seasonIDByNumber[s.SeasonNumber] = id
	}

	// 4. Episodes — wire season_id, batch upsert.
	for i := range m.Episodes {
		m.Episodes[i].SeriesID = seriesID
		if sid, ok := seasonIDByNumber[m.Episodes[i].SeasonNumber]; ok {
			m.Episodes[i].SeasonID = &sid
		}
	}
	episodeIDs, err := w.deps.Episodes.BatchUpsert(txCtx, m.Episodes)
	if err != nil {
		return nil, fmt.Errorf("batch upsert episodes: %w", err)
	}

	// 5. episode_texts — wire by parallel index from BatchUpsert.
	for i, txt := range m.EpisodeTexts {
		if i >= len(episodeIDs) {
			break
		}
		txt.EpisodeID = domain.EpisodeID(episodeIDs[i])
		if err := w.deps.EpisodeTexts.Upsert(txCtx, txt); err != nil {
			return nil, fmt.Errorf("upsert episode_texts: %w", err)
		}
	}

	// 6. People stubs — upsert each, build (tmdb_person_id → person_id) map.
	personIDByTMDB := make(map[int]int64, len(m.PersonStubs))
	for _, st := range m.PersonStubs {
		pid, err := w.deps.People.Upsert(txCtx, st)
		if err != nil {
			return nil, fmt.Errorf("upsert person stub: %w", err)
		}
		if st.TMDBID != nil {
			personIDByTMDB[int(*st.TMDBID)] = pid
		}
	}

	// 7. series_people — re-walk the TV aggregate_credits payload to
	//    pair each credit row with the TMDB person id, then resolve
	//    against personIDByTMDB. The mapper-produced SeriesCredit
	//    slice does NOT carry the TMDB person id (see infrastructure/
	//    tmdb/mappers.go::MapTVToCredits comment); re-walking is the
	//    cheapest correlation path that does NOT change the mapper
	//    surface. Credits whose person we failed to upsert (defensive)
	//    are dropped + counted.
	finalCredits, droppedCredits := resolveSeriesCreditsWithPersonID(tv, seriesID, personIDByTMDB)
	if len(finalCredits) > 0 {
		if _, err := w.deps.SeriesPeople.BatchUpsert(txCtx, finalCredits); err != nil {
			return nil, fmt.Errorf("batch upsert series_people: %w", err)
		}
	}
	if droppedCredits > 0 {
		log.WarnContext(txCtx, "enrichment.series.handle.credits_dropped",
			slog.Int("dropped", droppedCredits),
			slog.Int("kept", len(finalCredits)),
		)
	}

	// 7b. 212: build the post-tx person-enqueue list from the cast +
	//     crew rows. Cast carries credit_order (TMDB billing index) —
	//     the worker passes <10 to PriorityHot, ≥10 to PriorityCold
	//     downstream. Crew has no billing order; sentinel 999 forces
	//     cold for every crew row.
	enqueueRows := make([]personEnqueueRow, 0, len(personIDByTMDB))
	if tv.AggregateCredits != nil {
		for _, cast := range tv.AggregateCredits.Cast {
			pid, ok := personIDByTMDB[int(cast.ID)]
			if !ok || pid == 0 {
				continue
			}
			enqueueRows = append(enqueueRows, personEnqueueRow{
				PersonID:    pid,
				CreditOrder: cast.Order,
			})
		}
		for _, crew := range tv.AggregateCredits.Crew {
			pid, ok := personIDByTMDB[int(crew.ID)]
			if !ok || pid == 0 {
				continue
			}
			enqueueRows = append(enqueueRows, personEnqueueRow{
				PersonID:    pid,
				CreditOrder: 999,
			})
		}
	}

	// 8. Taxonomy — Genres / Keywords / Networks / Companies upsert + Set.
	if err := w.applyTaxonomy(txCtx, seriesID, m); err != nil {
		return nil, err
	}

	// 9. Videos.
	for _, v := range m.Videos {
		v.SeriesID = seriesID
		if err := w.deps.Videos.Upsert(txCtx, v); err != nil {
			return nil, fmt.Errorf("upsert video: %w", err)
		}
	}

	// 10. Content ratings.
	for _, cr := range m.ContentRatings {
		if err := w.deps.ContentRatings.Upsert(txCtx, seriesID, cr.Country, cr.Rating); err != nil {
			return nil, fmt.Errorf("upsert content_rating: %w", err)
		}
	}

	// 11. External IDs.
	for _, e := range m.ExternalIDs {
		if err := w.deps.ExternalIDs.Upsert(txCtx, enrichment.EntityTypeSeries, int64(seriesID), e.Provider, e.ProviderID); err != nil {
			return nil, fmt.Errorf("upsert external_id: %w", err)
		}
	}

	// 12. Recommendations — upsert each stub by tmdb_id, collect
	//     canon ids, write the join via Set. Story 319: stubs go
	//     through UpsertStub, whose ON CONFLICT preserves existing
	//     poster_asset / backdrop_asset / hydration='full' so a
	//     recommendation sweep cannot blank out a real canon row.
	recIDs := make([]domain.SeriesID, 0, len(m.Recommendations))
	for _, rec := range m.Recommendations {
		id, err := w.deps.Series.UpsertStub(txCtx, rec)
		if err != nil {
			return nil, fmt.Errorf("upsert recommendation stub: %w", err)
		}
		// Skip self-references defensively (the recommendations Set
		// rejects recommended_series_id == series_id).
		if id == seriesID {
			continue
		}
		recIDs = append(recIDs, id)
	}
	if err := w.deps.Recommendations.Set(txCtx, seriesID, recIDs); err != nil {
		return nil, fmt.Errorf("set recommendations: %w", err)
	}
	return enqueueRows, nil
}

// resolveSeriesCreditsWithPersonID re-walks tv.AggregateCredits and
// pairs every credit with its TMDB person id, then resolves the
// person id via personIDByTMDB. Order MUST match MapTVToCredits
// exactly — cast first (one row per cast member), then crew (one
// row per job per crew member). Returns the resolved slice + a
// drop count for any credit we cannot wire.
func resolveSeriesCreditsWithPersonID(tv *tmdb.TVResponse, seriesID domain.SeriesID, personIDByTMDB map[int]int64) ([]people.SeriesCredit, int) {
	if tv == nil || tv.AggregateCredits == nil {
		return nil, 0
	}
	out := make([]people.SeriesCredit, 0, len(tv.AggregateCredits.Cast)+len(tv.AggregateCredits.Crew))
	dropped := 0
	for _, cast := range tv.AggregateCredits.Cast {
		pid, ok := personIDByTMDB[int(cast.ID)]
		if !ok || pid == 0 {
			dropped++
			continue
		}
		creditID := ""
		var characterPtr *string
		if len(cast.Roles) > 0 {
			creditID = cast.Roles[0].CreditID
			if cast.Roles[0].Character != "" {
				ch := cast.Roles[0].Character
				characterPtr = &ch
			}
		}
		if creditID == "" {
			dropped++
			continue
		}
		order := cast.Order
		var episodeCountPtr *int
		if cast.TotalEpisodeCount > 0 {
			ec := cast.TotalEpisodeCount
			episodeCountPtr = &ec
		}
		out = append(out, people.SeriesCredit{
			SeriesID:      seriesID,
			PersonID:      pid,
			Kind:          people.SeriesCreditCast,
			TMDBCreditID:  creditID,
			CharacterName: characterPtr,
			CreditOrder:   &order,
			EpisodeCount:  episodeCountPtr,
		})
	}
	for _, crew := range tv.AggregateCredits.Crew {
		pid, ok := personIDByTMDB[int(crew.ID)]
		if !ok || pid == 0 {
			dropped += len(crew.Jobs)
			continue
		}
		var deptPtr *string
		if crew.Department != "" {
			d := crew.Department
			deptPtr = &d
		}
		for _, job := range crew.Jobs {
			if job.CreditID == "" {
				dropped++
				continue
			}
			var jobPtr *string
			if job.Job != "" {
				j := job.Job
				jobPtr = &j
			}
			var episodeCountPtr *int
			if job.EpisodeCount > 0 {
				ec := job.EpisodeCount
				episodeCountPtr = &ec
			}
			out = append(out, people.SeriesCredit{
				SeriesID:     seriesID,
				PersonID:     pid,
				Kind:         people.SeriesCreditCrew,
				TMDBCreditID: job.CreditID,
				Department:   deptPtr,
				Job:          jobPtr,
				EpisodeCount: episodeCountPtr,
			})
		}
	}
	return out, dropped
}

func (w *SeriesWorker) applyTaxonomy(txCtx context.Context, seriesID domain.SeriesID, m mappedPayload) error {
	gIDs := make([]int64, 0, len(m.Genres))
	for _, g := range m.Genres {
		id, err := w.deps.Genres.Upsert(txCtx, g)
		if err != nil {
			return fmt.Errorf("upsert genre: %w", err)
		}
		if g.Name != "" {
			if err := w.deps.Genres.UpsertI18n(txCtx, id, w.deps.Language, g.Name); err != nil {
				return fmt.Errorf("upsert genres_i18n: %w", err)
			}
		}
		gIDs = append(gIDs, id)
	}
	if err := w.deps.Genres.Set(txCtx, seriesID, gIDs); err != nil {
		return fmt.Errorf("set series_genres: %w", err)
	}

	kIDs := make([]int64, 0, len(m.Keywords))
	for _, k := range m.Keywords {
		id, err := w.deps.Keywords.Upsert(txCtx, k)
		if err != nil {
			return fmt.Errorf("upsert keyword: %w", err)
		}
		if k.Name != "" {
			if err := w.deps.Keywords.UpsertI18n(txCtx, id, w.deps.Language, k.Name); err != nil {
				return fmt.Errorf("upsert keywords_i18n: %w", err)
			}
		}
		kIDs = append(kIDs, id)
	}
	if err := w.deps.Keywords.Set(txCtx, seriesID, kIDs); err != nil {
		return fmt.Errorf("set series_keywords: %w", err)
	}

	nIDs := make([]int64, 0, len(m.Networks))
	for _, n := range m.Networks {
		id, err := w.deps.Networks.Upsert(txCtx, n)
		if err != nil {
			return fmt.Errorf("upsert network: %w", err)
		}
		nIDs = append(nIDs, id)
	}
	if err := w.deps.Networks.Set(txCtx, seriesID, nIDs); err != nil {
		return fmt.Errorf("set series_networks: %w", err)
	}

	cIDs := make([]int64, 0, len(m.Companies))
	for _, c := range m.Companies {
		id, err := w.deps.Companies.Upsert(txCtx, c)
		if err != nil {
			return fmt.Errorf("upsert company: %w", err)
		}
		cIDs = append(cIDs, id)
	}
	if err := w.deps.Companies.Set(txCtx, seriesID, cIDs); err != nil {
		return fmt.Errorf("set series_companies: %w", err)
	}
	return nil
}

// ---- error handling + journal helpers ------------------------------

// handleTMDBError journals outcome=error (with backoff) for retryable
// failures OR outcome=not_found for TMDB 404. Returns nil — the
// dispatcher only cares about success/failure for slog; the
// journalled outcome drives the retry sweep.
func (w *SeriesWorker) handleTMDBError(ctx context.Context, seriesID domain.SeriesID, op string, err error, previousAttempts int, start time.Time) error {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypeSeries)),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("source", string(enrichment.SourceTMDBSeries)),
		slog.String("op", op),
	)

	var apiErr *tmdb.APIError
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		// Terminal — not_found.
		ed := err.Error()
		entry := enrichment.SyncLog{
			EntityType:  enrichment.EntityTypeSeries,
			EntityID:    int64(seriesID),
			Source:      enrichment.SourceTMDBSeries,
			SyncedAt:    nil,
			Outcome:     enrichment.OutcomeNotFound,
			ErrorDetail: &ed,
			Attempts:    previousAttempts + 1,
			DurationMs:  &durMs,
		}
		if jerr := w.deps.SyncLog.Upsert(ctx, entry); jerr != nil {
			log.WarnContext(ctx, "enrichment.series.handle.journal_failed",
				slog.String("outcome", "not_found"),
				slog.String("error", jerr.Error()))
		}
		log.InfoContext(ctx, "enrichment.series.handle.not_found",
			slog.String("outcome", string(enrichment.OutcomeNotFound)),
			slog.Int("duration_ms", durMs),
		)
		return nil
	}

	// Retryable — journal outcome=error + NextAttemptAt.
	attempts := previousAttempts + 1
	next := enrichment.NextAttemptAt(attempts, now)
	ed := err.Error()
	entry := enrichment.SyncLog{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      int64(seriesID),
		Source:        enrichment.SourceTMDBSeries,
		SyncedAt:      nil,
		Outcome:       enrichment.OutcomeError,
		ErrorDetail:   &ed,
		Attempts:      attempts,
		NextAttemptAt: &next,
		DurationMs:    &durMs,
	}
	if jerr := w.deps.SyncLog.Upsert(ctx, entry); jerr != nil {
		log.WarnContext(ctx, "enrichment.series.handle.journal_failed",
			slog.String("outcome", "error"),
			slog.String("error", jerr.Error()))
	}
	log.WarnContext(ctx, "enrichment.series.handle.failed",
		slog.String("outcome", string(enrichment.OutcomeError)),
		slog.Int("attempts", attempts),
		slog.Time("next_attempt_at", next),
		slog.Int("duration_ms", durMs),
		slog.String("error", err.Error()),
	)
	return nil
}

func (w *SeriesWorker) journalOK(ctx context.Context, seriesID domain.SeriesID, now time.Time, durMs int) {
	entry := enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(seriesID),
		Source:     enrichment.SourceTMDBSeries,
		SyncedAt:   &now,
		Outcome:    enrichment.OutcomeOK,
		Attempts:   0, // reset on success per PRD §5.5
		DurationMs: &durMs,
	}
	if err := w.deps.SyncLog.Upsert(ctx, entry); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.series.handle.journal_ok_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
}

func (w *SeriesWorker) journalNotFound(ctx context.Context, seriesID domain.SeriesID, msg string, start time.Time) {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	ed := msg
	entry := enrichment.SyncLog{
		EntityType:  enrichment.EntityTypeSeries,
		EntityID:    int64(seriesID),
		Source:      enrichment.SourceTMDBSeries,
		Outcome:     enrichment.OutcomeNotFound,
		ErrorDetail: &ed,
		Attempts:    1,
		DurationMs:  &durMs,
	}
	if err := w.deps.SyncLog.Upsert(ctx, entry); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.series.handle.journal_nf_failed",
			slog.Int64("entity_id", int64(seriesID)),
			slog.String("error", err.Error()))
	}
}

// ---- mapping helpers (private) -------------------------------------

func patchFromTMDBCanon(c series.Canon) enrichment.SeriesPatch {
	return enrichment.SeriesPatch{
		TMDBID: tmdbIDPtrToInt(c.TMDBID), TVDBID: tvdbIDPtrToInt(c.TVDBID), IMDBID: imdbIDPtrToString(c.IMDBID),
		OriginalTitle: c.OriginalTitle, Status: c.Status,
		FirstAirDate: c.FirstAirDate, LastAirDate: c.LastAirDate,
		NextAirDate:      c.NextAirDate,
		Homepage:         c.Homepage,
		OriginalLanguage: c.OriginalLanguage,
		OriginCountry:    c.OriginCountry,
		OriginCountries:  append([]string(nil), c.OriginCountries...),
		Popularity:       c.Popularity,
		InProduction:     &c.InProduction,
		PosterAsset:      c.PosterAsset,
		BackdropAsset:    c.BackdropAsset,
		TMDBRating:       c.TMDBRating,
		TMDBVotes:        c.TMDBVotes,
		RuntimeMinutes:   c.RuntimeMinutes,
		Title:            nonEmptyStringPtr(c.Title),
	}
}

func nonEmptyStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// tvdbIDPtrToInt / intPtrToTVDBID bridge the typed TVDBID seam
// between series.Canon (*domain.TVDBID) and the domain/enrichment
// patch + canon shapes (*int). The enrichment package intentionally
// stays import-free of internal/shared/domain so the application
// layer does the cast at the merge seam.
func tvdbIDPtrToInt(p *domain.TVDBID) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func intPtrToTVDBID(p *int) *domain.TVDBID {
	if p == nil {
		return nil
	}
	v := domain.TVDBID(*p)
	return &v
}

// tmdbIDPtrToInt / intPtrToTMDBID bridge the typed TMDBID seam between
// series.Canon (*domain.TMDBID) and the domain/enrichment patch + canon
// shapes (*int). Same rationale as the TVDB bridge — domain/enrichment
// intentionally stays import-free of internal/shared/domain. Story 403
// A-5d-2.
func tmdbIDPtrToInt(p *domain.TMDBID) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func intPtrToTMDBID(p *int) *domain.TMDBID {
	if p == nil {
		return nil
	}
	v := domain.TMDBID(*p)
	return &v
}

// imdbIDPtrToString / stringPtrToIMDBID bridge the typed IMDBID seam
// between series.Canon (*domain.IMDBID) and the domain/enrichment
// patch + canon shapes (*string). Same rationale as the TVDB bridge:
// domain/enrichment intentionally stays import-free of
// internal/shared/domain so the application layer does the cast at
// the merge seam. Story 402 A-5d-1.
func imdbIDPtrToString(p *domain.IMDBID) *string {
	if p == nil {
		return nil
	}
	v := string(*p)
	return &v
}

func stringPtrToIMDBID(p *string) *domain.IMDBID {
	if p == nil {
		return nil
	}
	v := domain.IMDBID(*p)
	return &v
}

func canonToEnrichmentCanon(c series.Canon) enrichment.SeriesCanon {
	return enrichment.SeriesCanon{
		Hydration: enrichment.HydrationLevel(c.Hydration),
		TMDBID:    tmdbIDPtrToInt(c.TMDBID), TVDBID: tvdbIDPtrToInt(c.TVDBID), IMDBID: imdbIDPtrToString(c.IMDBID),
		Title: c.Title, OriginalTitle: c.OriginalTitle, Status: c.Status,
		FirstAirDate: c.FirstAirDate, LastAirDate: c.LastAirDate,
		NextAirDate: c.NextAirDate, Year: c.Year,
		RuntimeMinutes: c.RuntimeMinutes, Homepage: c.Homepage,
		OriginalLanguage: c.OriginalLanguage, OriginCountry: c.OriginCountry, OriginCountries: append([]string(nil), c.OriginCountries...),
		Popularity: c.Popularity, InProduction: c.InProduction,
		PosterAsset: c.PosterAsset, BackdropAsset: c.BackdropAsset,
		TMDBRating: c.TMDBRating, TMDBVotes: c.TMDBVotes,
		IMDBRating: c.IMDBRating, IMDBVotes: c.IMDBVotes,
		OMDBRated: c.OMDBRated, OMDBAwards: c.OMDBAwards,
	}
}

func enrichmentCanonToCanon(ec enrichment.SeriesCanon, base series.Canon) series.Canon {
	base.Hydration = series.Hydration(ec.Hydration)
	base.TMDBID = intPtrToTMDBID(ec.TMDBID)
	base.TVDBID = intPtrToTVDBID(ec.TVDBID)
	base.IMDBID = stringPtrToIMDBID(ec.IMDBID)
	base.Title = ec.Title
	base.OriginalTitle = ec.OriginalTitle
	base.Status = ec.Status
	base.FirstAirDate = ec.FirstAirDate
	base.LastAirDate = ec.LastAirDate
	base.NextAirDate = ec.NextAirDate
	base.Year = ec.Year
	base.RuntimeMinutes = ec.RuntimeMinutes
	base.Homepage = ec.Homepage
	base.OriginalLanguage = ec.OriginalLanguage
	base.OriginCountry = ec.OriginCountry
	base.OriginCountries = append([]string(nil), ec.OriginCountries...)
	base.Popularity = ec.Popularity
	base.InProduction = ec.InProduction
	base.PosterAsset = ec.PosterAsset
	base.BackdropAsset = ec.BackdropAsset
	base.TMDBRating = ec.TMDBRating
	base.TMDBVotes = ec.TMDBVotes
	base.IMDBRating = ec.IMDBRating
	base.IMDBVotes = ec.IMDBVotes
	base.OMDBRated = ec.OMDBRated
	base.OMDBAwards = ec.OMDBAwards
	return base
}

func findEpisodeTitle(sr *tmdb.SeasonResponse, seasonNumber, episodeNumber int) string {
	if sr == nil {
		return ""
	}
	for _, e := range sr.Episodes {
		if e.SeasonNumber == seasonNumber && e.EpisodeNumber == episodeNumber {
			return e.Name
		}
	}
	return ""
}

func findEpisodeOverview(sr *tmdb.SeasonResponse, seasonNumber, episodeNumber int) string {
	if sr == nil {
		return ""
	}
	for _, e := range sr.Episodes {
		if e.SeasonNumber == seasonNumber && e.EpisodeNumber == episodeNumber {
			return e.Overview
		}
	}
	return ""
}
