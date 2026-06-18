// Package scan — SyncSeriesFromSonarr (E-1, Story 210) per-series sync
// pipeline: pull the rich Sonarr payload, resolve-or-create the canonical
// series row, apply MergeSeries (story 207), upsert taxonomy joins,
// fan out per-episode canon + per-instance state writes.
//
// Two-instance invariant (PRD §5.11): two Sonarr instances of the same
// show converge on one series row, one set of series_genres / series_networks,
// one set of episodes / episode_texts; the per-instance projection lives
// in series_cache.Upsert and episode_states.Upsert keyed on
// (instance_name, episode_id).
package scan

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SyncDeps is the dependency set for SyncSeriesFromSonarr. All ports
// are required (Logger may be nil — defaults to slog.Default).
// PostSync is OPTIONAL: when non-nil, SyncSeriesFromSonarr invokes
// it after the canonical series row is persisted with the resolved
// canon series.id. Story 211 (C-2) uses this hook to enqueue the
// freshly-synced series for TMDB enrichment without dragging the
// dispatcher's enqueue API into the scan layer.
type SyncDeps struct {
	Series        SeriesCanonRepository
	SeriesCache   SyncSeriesCacheRepository
	Episodes      EpisodesRepository
	EpisodeStates EpisodeStatesRepository
	EpisodeTexts  EpisodeTextsRepository
	// SeasonStats — story 377. Nil-OK for tests / older callers; when
	// set, SyncSeriesFromSonarr writes one row per Sonarr season alongside
	// the series_cache upsert.
	SeasonStats SeasonStatsRepository
	Genres      GenresPort
	Networks    NetworksPort
	Logger      *slog.Logger
	PostSync    func(ctx context.Context, seriesID domain.SeriesID)
}

// SonarrPayloadBundle groups the three Sonarr fetches the sync needs.
// Constructed by the caller (webhook handler, scan loop, etc.).
type SonarrPayloadBundle struct {
	Series       sonarr.SeriesPayload
	Episodes     []sonarr.EpisodePayload
	EpisodeFiles []sonarr.EpisodeFilePayload
}

// SyncSeriesFromSonarr lands one Sonarr series + episodes for one
// instance. Returns the resolved canon series.id (for diagnostics)
// and any error from a hard failure.
func SyncSeriesFromSonarr(
	ctx context.Context,
	deps SyncDeps,
	instanceName domain.InstanceName,
	bundle SonarrPayloadBundle,
) (domain.SeriesID, error) {
	if instanceName == "" {
		return 0, fmt.Errorf("sync sonarr series: instance_name must be non-empty")
	}
	p := bundle.Series
	if p.ID == 0 {
		return 0, fmt.Errorf("sync sonarr series: payload missing sonarr id")
	}
	logger := deps.Logger
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "scan")
	}
	log := logger.With(
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(p.ID)),
		slog.Int("tmdb_id", p.TMDBID),
		slog.Int("tvdb_id", p.TVDBID),
	)

	canon, err := ResolveOrCreateSeries(ctx, deps.Series, p)
	if err != nil {
		return 0, fmt.Errorf("sync sonarr series: %w", err)
	}

	merged := enrichment.MergeSeries(
		canonToEnrichmentCanon(canon),
		sonarrPatchFromPayload(p),
		enrichment.SourceSonarr,
	)
	canonOut := enrichmentCanonToCanon(merged, canon)
	canonID, err := deps.Series.Upsert(ctx, canonOut)
	if err != nil {
		return 0, fmt.Errorf("sync sonarr series: upsert canon: %w", err)
	}
	log = log.With(slog.Int64("canon_series_id", int64(canonID)))

	if err := syncGenres(ctx, deps, canonID, p.Genres, log); err != nil {
		log.WarnContext(ctx, "sync_sonarr_genres_failed", slog.String("error", err.Error()))
	}
	if err := syncNetwork(ctx, deps, canonID, p.Network, log); err != nil {
		log.WarnContext(ctx, "sync_sonarr_network_failed", slog.String("error", err.Error()))
	}

	if err := deps.SeriesCache.Upsert(ctx, cacheEntryFromPayload(instanceName, p)); err != nil {
		return canonID, fmt.Errorf("sync sonarr series: cache upsert: %w", err)
	}

	if deps.SeasonStats != nil {
		for _, s := range p.Seasons {
			stat := series.SeasonStat{
				InstanceName:      instanceName,
				SonarrSeriesID:    p.ID,
				SeasonNumber:      s.Number,
				Monitored:         s.Monitored,
				EpisodeCount:      s.Statistics.EpisodeCount,
				EpisodeFileCount:  s.Statistics.EpisodeFileCount,
				TotalEpisodeCount: s.Statistics.Total,
				AiredEpisodeCount: s.Statistics.Aired,
				SizeOnDiskBytes:   s.Statistics.SizeOnDisk,
			}
			if uerr := deps.SeasonStats.Upsert(ctx, stat); uerr != nil {
				// season_stats is best-effort for one season — a single
				// row failure must NOT abort the whole sync (episodes
				// still need to land). Warn-log and continue.
				log.WarnContext(ctx, "sync_sonarr_season_stats_upsert_failed",
					slog.Int("season_number", s.Number),
					slog.String("error", uerr.Error()))
			}
		}
	}

	if len(bundle.Episodes) > 0 {
		if err := syncEpisodes(ctx, deps, canonID, instanceName, bundle, log); err != nil {
			return canonID, fmt.Errorf("sync sonarr series: episodes: %w", err)
		}
	}

	if deps.PostSync != nil {
		deps.PostSync(ctx, canonID)
	}

	log.InfoContext(ctx, "sync_sonarr_series_ok",
		slog.Int("episodes", len(bundle.Episodes)),
		slog.Int("episode_files", len(bundle.EpisodeFiles)),
	)
	return canonID, nil
}

// sonarrPatchFromPayload extracts Sonarr-supplied fields into a typed
// SeriesPatch. Empty / zero-value fields stay nil.
func sonarrPatchFromPayload(p sonarr.SeriesPayload) enrichment.SeriesPatch {
	patch := enrichment.SeriesPatch{}
	if p.Title != "" {
		v := p.Title
		patch.Title = &v
	}
	if p.Year > 0 {
		v := p.Year
		patch.Year = &v
	}
	if p.Status != "" {
		v := p.Status
		patch.Status = &v
	}
	if p.Runtime > 0 {
		v := p.Runtime
		patch.RuntimeMinutes = &v
	}
	if p.NextAiring != nil {
		v := p.NextAiring.UTC()
		patch.NextAirDate = &v
	}
	if p.FirstAired != nil {
		v := p.FirstAired.UTC()
		patch.FirstAirDate = &v
	}
	if p.LastAired != nil {
		v := p.LastAired.UTC()
		patch.LastAirDate = &v
	}
	if p.TMDBID > 0 {
		v := p.TMDBID
		patch.TMDBID = &v
	}
	if p.TVDBID > 0 {
		v := p.TVDBID
		patch.TVDBID = &v
	}
	if p.IMDBID != "" {
		v := p.IMDBID
		patch.IMDBID = &v
	}
	return patch
}

func canonToEnrichmentCanon(c series.Canon) enrichment.SeriesCanon {
	return enrichment.SeriesCanon{
		Hydration:        enrichment.HydrationLevel(c.Hydration),
		TMDBID:           c.TMDBID,
		TVDBID:           c.TVDBID,
		IMDBID:           c.IMDBID,
		Title:            c.Title,
		OriginalTitle:    c.OriginalTitle,
		Status:           c.Status,
		FirstAirDate:     c.FirstAirDate,
		LastAirDate:      c.LastAirDate,
		NextAirDate:      c.NextAirDate,
		Year:             c.Year,
		RuntimeMinutes:   c.RuntimeMinutes,
		Homepage:         c.Homepage,
		OriginalLanguage: c.OriginalLanguage,
		OriginCountry:    c.OriginCountry,
		OriginCountries:  append([]string(nil), c.OriginCountries...),
		Popularity:       c.Popularity,
		InProduction:     c.InProduction,
		PosterAsset:      c.PosterAsset,
		BackdropAsset:    c.BackdropAsset,
		TMDBRating:       c.TMDBRating,
		TMDBVotes:        c.TMDBVotes,
		IMDBRating:       c.IMDBRating,
		IMDBVotes:        c.IMDBVotes,
		OMDBRated:        c.OMDBRated,
		OMDBAwards:       c.OMDBAwards,
	}
}

func enrichmentCanonToCanon(ec enrichment.SeriesCanon, base series.Canon) series.Canon {
	base.Hydration = series.Hydration(ec.Hydration)
	base.TMDBID = ec.TMDBID
	base.TVDBID = ec.TVDBID
	base.IMDBID = ec.IMDBID
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

// cacheEntryFromPayload constructs the thin per-instance projection.
// PRD §5.11: only title_slug / monitored / missing_count are owned by
// series_cache; everything else lives on the canon row. The legacy
// CacheEntry wire shape still carries Title / external ids because
// SeriesCacheRepository.Upsert reads them on the resolve path; after
// E-1 the merge policy has already populated canon, so the legacy
// resolveOrCreateCanon writes a redundant (but consistent) row.
func cacheEntryFromPayload(instanceName domain.InstanceName, p sonarr.SeriesPayload) series.CacheEntry {
	e := series.CacheEntry{
		InstanceName:   instanceName,
		SonarrSeriesID: p.ID,
		Title:          p.Title,
		TitleSlug:      p.TitleSlug,
		Monitored:      p.Monitored,
		MissingCount: series.Statistics{
			EpisodeCount:     p.Statistics.EpisodeCount,
			EpisodeFileCount: p.Statistics.EpisodeFileCount,
			Total:            p.Statistics.Total,
			Aired:            p.Statistics.Aired,
		}.AiredMissing(),
		EpisodeFileCount:  p.Statistics.EpisodeFileCount,
		SizeOnDiskBytes:   p.Statistics.SizeOnDisk,
		AiredEpisodeCount: airedOrEpisodeCount(p.Statistics),
	}
	if p.TMDBID > 0 {
		v := p.TMDBID
		e.TMDBID = &v
	}
	if p.TVDBID > 0 {
		v := p.TVDBID
		e.TVDBID = &v
	}
	if p.IMDBID != "" {
		v := p.IMDBID
		e.IMDBID = &v
	}
	if p.Status != "" {
		v := p.Status
		e.Status = &v
	}
	if p.Year > 0 {
		v := p.Year
		e.Year = &v
	}
	if p.Runtime > 0 {
		v := p.Runtime
		e.RuntimeMinutes = &v
	}
	if p.PreviousAiring != nil {
		v := *p.PreviousAiring
		e.LastAiredAt = &v
	}
	return e
}

// airedOrEpisodeCount mirrors the LIST-endpoint fallback in
// seriesDTOToCacheEntry (story 380): Sonarr's series-level statistics
// block omits airedEpisodeCount on /api/v3/series LIST responses;
// episodeCount carries the same semantic there (legacy v3 naming = aired
// count). Falling back keeps the LibraryStrip denominator non-zero on
// the scan path that pulls the LIST endpoint.
func airedOrEpisodeCount(s series.Statistics) int {
	if s.Aired > 0 {
		return s.Aired
	}
	return s.EpisodeCount
}

// syncGenres resolves every Sonarr-supplied genre string to a canon
// genres.id (creating + i18n on miss), then writes the series_genres
// join in one Set call. Empty input clears the join.
func syncGenres(ctx context.Context, deps SyncDeps, canonID domain.SeriesID, genres []string, log *slog.Logger) error {
	ids := make([]int64, 0, len(genres))
	for _, name := range genres {
		if name == "" {
			continue
		}
		id, err := deps.Genres.ResolveByName(ctx, "en-US", name)
		if err == nil {
			ids = append(ids, id)
			continue
		}
		newID, uerr := deps.Genres.Upsert(ctx, GenreStub{TMDBID: nil})
		if uerr != nil {
			log.WarnContext(ctx, "sync_sonarr_genre_create_failed",
				slog.String("genre", name),
				slog.String("error", uerr.Error()),
			)
			continue
		}
		if i18nErr := deps.Genres.UpsertI18n(ctx, newID, "en-US", name); i18nErr != nil {
			log.WarnContext(ctx, "sync_sonarr_genre_i18n_failed",
				slog.String("genre", name),
				slog.String("error", i18nErr.Error()),
			)
			continue
		}
		ids = append(ids, newID)
	}
	if err := deps.Genres.Set(ctx, canonID, ids); err != nil {
		return fmt.Errorf("set series_genres: %w", err)
	}
	return nil
}

// syncNetwork resolves the single Sonarr-supplied network string to
// a canon networks.id (creating on miss), then writes a one-row
// series_networks join (position=0). Empty input clears the join.
func syncNetwork(ctx context.Context, deps SyncDeps, canonID domain.SeriesID, network string, _ *slog.Logger) error {
	if network == "" {
		if err := deps.Networks.SetForSeries(ctx, canonID, nil); err != nil {
			return fmt.Errorf("clear series_networks: %w", err)
		}
		return nil
	}
	id, err := deps.Networks.ResolveByName(ctx, network)
	if err != nil {
		id, err = deps.Networks.UpsertByName(ctx, network)
		if err != nil {
			return fmt.Errorf("create network %q: %w", network, err)
		}
	}
	if err := deps.Networks.SetForSeries(ctx, canonID, []int64{id}); err != nil {
		return fmt.Errorf("set series_networks: %w", err)
	}
	return nil
}

// syncEpisodes walks the Sonarr episode bundle and writes:
//  1. episodes (canonical, batched) — merge policy applied per-row.
//  2. episode_texts(en-US) per row.
//  3. episode_states (per-instance) per row, deriving quality /
//     size / episode_file_id from the episode_files lookup.
func syncEpisodes(
	ctx context.Context,
	deps SyncDeps,
	canonSeriesID domain.SeriesID,
	instanceName domain.InstanceName,
	bundle SonarrPayloadBundle,
	log *slog.Logger,
) error {
	existing, err := deps.Episodes.ListBySeries(ctx, canonSeriesID)
	if err != nil {
		return fmt.Errorf("load canon episodes: %w", err)
	}
	byNK := make(map[episodeNaturalKey]series.CanonEpisode, len(existing))
	for _, ep := range existing {
		byNK[episodeNaturalKey{Season: ep.SeasonNumber, Episode: ep.EpisodeNumber}] = ep
	}

	merged := make([]series.CanonEpisode, 0, len(bundle.Episodes))
	for _, ep := range bundle.Episodes {
		base, found := byNK[episodeNaturalKey{Season: ep.SeasonNumber, Episode: ep.EpisodeNumber}]
		if !found {
			base = series.CanonEpisode{
				SeriesID:      canonSeriesID,
				SeasonNumber:  ep.SeasonNumber,
				EpisodeNumber: ep.EpisodeNumber,
			}
		}
		ec := canonEpisodeToEnrichment(base)
		patch := sonarrEpisodePatch(ep)
		ec = enrichment.MergeEpisode(ec, patch, enrichment.SourceSonarr)
		merged = append(merged, enrichmentToCanonEpisode(ec, base, canonSeriesID))
	}

	ids, err := deps.Episodes.BatchUpsert(ctx, merged)
	if err != nil {
		return fmt.Errorf("batch upsert episodes: %w", err)
	}

	fileByID := make(map[int]sonarr.EpisodeFilePayload, len(bundle.EpisodeFiles))
	for _, f := range bundle.EpisodeFiles {
		fileByID[f.ID] = f
	}

	for i, ep := range bundle.Episodes {
		if i >= len(ids) {
			break
		}
		canonEpisodeID := ids[i]
		if canonEpisodeID == 0 {
			continue
		}
		text := series.EpisodeText{
			EpisodeID: canonEpisodeID,
			Language:  "en-US",
			Title:     stringPtrIfNotEmpty(ep.Title),
			Overview:  stringPtrIfNotEmpty(ep.Overview),
			UpdatedAt: time.Now().UTC(),
		}
		if text.Title != nil || text.Overview != nil {
			if terr := deps.EpisodeTexts.Upsert(ctx, text); terr != nil {
				log.WarnContext(ctx, "sync_sonarr_episode_text_failed",
					slog.Int64("episode_id", canonEpisodeID),
					slog.String("error", terr.Error()),
				)
			}
		}

		state := series.EpisodeState{
			InstanceName: instanceName,
			EpisodeID:    canonEpisodeID,
			Monitored:    ep.Monitored,
			HasFile:      ep.HasFile,
			UpdatedAt:    time.Now().UTC(),
		}
		if ep.EpisodeFileID > 0 {
			v := ep.EpisodeFileID
			state.EpisodeFileID = &v
			if f, ok := fileByID[ep.EpisodeFileID]; ok {
				if f.QualityName != "" {
					qn := f.QualityName
					state.Quality = &qn
				}
				if f.SizeBytes > 0 {
					sb := f.SizeBytes
					state.SizeBytes = &sb
				}
				if f.VideoCodec != "" {
					vc := f.VideoCodec
					state.VideoCodec = &vc
				}
				if f.AudioCodec != "" {
					ac := f.AudioCodec
					state.AudioCodec = &ac
				}
				if f.AudioChannels != "" {
					ach := f.AudioChannels
					state.AudioChannels = &ach
				}
				if f.ReleaseGroup != "" {
					rg := f.ReleaseGroup
					state.ReleaseGroup = &rg
				}
			}
		}
		if serr := deps.EpisodeStates.Upsert(ctx, state); serr != nil {
			return fmt.Errorf("upsert episode_state season=%d episode=%d: %w",
				ep.SeasonNumber, ep.EpisodeNumber, serr)
		}
	}
	return nil
}

type episodeNaturalKey struct {
	Season  int
	Episode int
}

func sonarrEpisodePatch(ep sonarr.EpisodePayload) enrichment.EpisodePatch {
	patch := enrichment.EpisodePatch{}
	if ep.ID > 0 {
		v := ep.ID
		patch.SonarrEpisodeID = &v
	}
	if !ep.AirDateUTC.IsZero() {
		v := ep.AirDateUTC.UTC()
		patch.AirDate = &v
	}
	if ep.Runtime > 0 {
		v := ep.Runtime
		patch.RuntimeMinutes = &v
	}
	if ep.FinaleType != "" {
		v := ep.FinaleType
		patch.FinaleType = &v
	}
	if ep.AbsoluteNumber != nil {
		patch.AbsoluteNumber = ep.AbsoluteNumber
	}
	return patch
}

func canonEpisodeToEnrichment(e series.CanonEpisode) enrichment.EpisodeCanon {
	return enrichment.EpisodeCanon{
		SeasonNumber:      e.SeasonNumber,
		EpisodeNumber:     e.EpisodeNumber,
		TMDBEpisodeNumber: e.TMDBEpisodeNumber,
		TMDBEpisodeID:     e.TMDBEpisodeID,
		SonarrEpisodeID:   e.SonarrEpisodeID,
		AbsoluteNumber:    e.AbsoluteNumber,
		AirDate:           e.AirDate,
		RuntimeMinutes:    e.RuntimeMinutes,
		FinaleType:        e.FinaleType,
		StillAsset:        e.StillAsset,
		TMDBRating:        e.TMDBRating,
		TMDBVotes:         e.TMDBVotes,
	}
}

func enrichmentToCanonEpisode(ec enrichment.EpisodeCanon, base series.CanonEpisode, canonSeriesID domain.SeriesID) series.CanonEpisode {
	base.SeriesID = canonSeriesID
	base.SeasonNumber = ec.SeasonNumber
	base.EpisodeNumber = ec.EpisodeNumber
	base.TMDBEpisodeNumber = ec.TMDBEpisodeNumber
	base.TMDBEpisodeID = ec.TMDBEpisodeID
	base.SonarrEpisodeID = ec.SonarrEpisodeID
	base.AbsoluteNumber = ec.AbsoluteNumber
	base.AirDate = ec.AirDate
	base.RuntimeMinutes = ec.RuntimeMinutes
	base.FinaleType = ec.FinaleType
	base.StillAsset = ec.StillAsset
	base.TMDBRating = ec.TMDBRating
	base.TMDBVotes = ec.TMDBVotes
	return base
}

func stringPtrIfNotEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
