package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesDetailHandler serves the composite series-detail document
// powering the SPA's series page (PRD §5.6, story 215).
//
// GET /api/v1/instances/:name/series/:id?lang=
type SeriesDetailHandler struct {
	composer *seriesdetail.Composer
	logger   *slog.Logger
}

func NewSeriesDetailHandler(composer *seriesdetail.Composer, logger *slog.Logger) *SeriesDetailHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesDetailHandler{composer: composer, logger: logger}
}

// Get handles GET /api/v1/instances/:name/series/:id.
//
// @Summary     Composite series detail document
// @Description Returns the full Series Detail Page payload — series
// @Description hero, library tile, seasons accordion, cast, recommendations,
// @Description taxonomy, external links — composed from the local entity
// @Description tables in one call. Each section is independently degradable:
// @Description a failed enrichment source surfaces as a `degraded[]` entry
// @Description with the affected section's data falling back to nil/empty
// @Description (NEVER 5xx). The single live call is the local Sonarr /queue
// @Description for the in-flight download chip — unreachable Sonarr also
// @Description surfaces via `degraded[]`.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true   "Instance name"
// @Param       id    path      int     true   "Sonarr series id (per-instance)"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  dto.SeriesDetailResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series/{id} [get]
func (h *SeriesDetailHandler) Get(c *gin.Context) {
	name := c.Param("name")
	idStr := c.Param("id")
	parsedID, err := strconv.Atoi(idStr)
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	sonarrID := domain.SonarrSeriesID(parsedID)
	lang := strings.TrimSpace(c.Query("lang"))

	ctx := c.Request.Context()
	detail, err := h.composer.Get(ctx, domain.InstanceName(name), sonarrID, lang)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toSeriesDetailResponse(detail))
}

// toSeriesDetailResponse maps the composer's domain object onto
// the locked-down DTO. No DB / network calls here — pure projection.
func toSeriesDetailResponse(d *seriesdetail.Detail) dto.SeriesDetailResponse {
	resp := dto.SeriesDetailResponse{
		Instance:        d.Instance,
		SonarrSeriesID:  d.SonarrSeriesID,
		SeriesID:        d.SeriesID,
		Lang:            d.Lang,
		Hero:            mapHero(d),
		Library:         mapLibrary(d),
		Download:        mapDownload(d),
		Overview:        mapOverview(d),
		Recent:          mapRecent(d.Recent),
		Torrents:        dto.TorrentsHint{SyncPending: d.Torrents.SyncPending, Count: d.Torrents.Count, TotalSizeBytes: d.Torrents.TotalSizeBytes},
		Seasons:         mapSeasons(d),
		Cast:            mapCast(d.Cast),
		Recommendations: mapRecommendations(d.Recommendations),
		ExternalLinks:   mapExternalLinks(d.ExternalIDs, d.Canon),
		Degraded:        sourceStringSlice(d.Degraded),
		SyncedAt:        d.SyncedAt,
	}
	return resp
}

func mapHero(d *seriesdetail.Detail) dto.SeriesHero {
	h := dto.SeriesHero{
		Title:          d.Canon.Title,
		OriginalTitle:  d.Canon.OriginalTitle,
		Status:         mapStatusPill(d.Canon.Status, d.Canon.InProduction),
		RuntimeMinutes: d.Canon.RuntimeMinutes,
		PosterAsset:    d.Canon.PosterAsset,
		BackdropAsset:  d.Canon.BackdropAsset,
		Genres:         []dto.TaxonomyChip{},
		Networks:       []dto.NetworkChip{},
	}
	if d.Canon.Year != nil {
		ys := *d.Canon.Year
		h.YearStart = &ys
	}
	if d.Canon.LastAirDate != nil {
		ye := d.Canon.LastAirDate.Year()
		h.YearEnd = &ye
	}
	if d.Text != nil {
		if d.Text.Title != nil && *d.Text.Title != "" {
			h.Title = *d.Text.Title
			h.TitleLanguage = d.Text.Language
		}
		h.Tagline = d.Text.Tagline
	}
	if d.Canon.TMDBRating != nil {
		votes := 0
		if d.Canon.TMDBVotes != nil {
			votes = *d.Canon.TMDBVotes
		}
		h.TMDBRating = &dto.RatingScore{Score: *d.Canon.TMDBRating, Votes: votes}
	}
	if d.Canon.IMDBRating != nil {
		votes := 0
		if d.Canon.IMDBVotes != nil {
			votes = *d.Canon.IMDBVotes
		}
		h.IMDbRating = &dto.RatingScore{Score: *d.Canon.IMDBRating, Votes: votes}
	}
	for _, g := range d.Genres {
		h.Genres = append(h.Genres, dto.TaxonomyChip{ID: g.ID, Name: g.Name, Language: g.Language})
	}
	for _, n := range d.Networks {
		h.Networks = append(h.Networks, dto.NetworkChip{ID: n.ID, Name: n.Name, LogoAsset: n.LogoAsset})
	}
	if len(d.Companies) > 0 && d.Companies[0].Name != "" {
		name := d.Companies[0].Name
		h.Studio = &name
	}
	if d.Canon.OriginCountry != nil && *d.Canon.OriginCountry != "" {
		c := *d.Canon.OriginCountry
		h.Country = &c
	}
	if len(d.Canon.OriginCountries) > 0 {
		h.Countries = append([]string(nil), d.Canon.OriginCountries...)
		// Backfill singular Country from Countries[0] when canon.origin_country
		// happened to be NULL but the array carries data (defensive — the TMDB
		// mapper writes both, but cold rows may diverge).
		if h.Country == nil && d.Canon.OriginCountries[0] != "" {
			c := d.Canon.OriginCountries[0]
			h.Country = &c
		}
	}
	if d.Canon.FirstAirDate != nil {
		s := d.Canon.FirstAirDate.Format("2006-01-02")
		h.PremiereDate = &s
	}
	if d.Canon.OriginalLanguage != nil && *d.Canon.OriginalLanguage != "" {
		lang := *d.Canon.OriginalLanguage
		h.OriginalLanguage = &lang
	}
	if d.ContentRating != nil {
		h.ContentRating = &dto.ContentRatingBadge{CountryCode: d.ContentRating.CountryCode, Rating: d.ContentRating.Rating}
	}
	if d.Trailer != nil {
		var site, key, name string
		if d.Trailer.Site != nil {
			site = *d.Trailer.Site
		}
		if d.Trailer.Key != nil {
			key = *d.Trailer.Key
		}
		name = d.Trailer.Name
		h.Trailer = &dto.Trailer{Site: site, Key: key, Name: name, PublishedAt: d.Trailer.PublishedAt}
	}
	// Story 373: prefer the composer's pick from d.Seasons[].Episodes[].
	// Falls back to canon.next_air_date when the episode-table scan came
	// up empty (truly cold series or seasons branch degraded).
	if d.NextEpisode != nil {
		h.NextEpisode = &dto.NextEpisode{
			SeasonNumber:  d.NextEpisode.SeasonNumber,
			EpisodeNumber: d.NextEpisode.EpisodeNumber,
			Title:         d.NextEpisode.Title,
			AirDate:       d.NextEpisode.AirDate,
		}
	} else if d.Canon.NextAirDate != nil {
		h.NextEpisode = &dto.NextEpisode{AirDate: d.Canon.NextAirDate}
	}
	return h
}

// mapStatusPill projects upstream status strings + the InProduction
// flag onto the design-brief's status enum. Frontend renders the
// pill colour from this token.
func mapStatusPill(status *string, inProduction bool) string {
	raw := ""
	if status != nil {
		raw = strings.ToLower(strings.TrimSpace(*status))
	}
	switch {
	case strings.Contains(raw, "cancel"):
		return "canceled"
	case strings.Contains(raw, "ended"):
		return "ended"
	case strings.Contains(raw, "upcoming") || strings.Contains(raw, "planned"):
		return "upcoming"
	case strings.Contains(raw, "production") && !strings.Contains(raw, "post"):
		return "in_production"
	case strings.Contains(raw, "continu") || strings.Contains(raw, "ongoing") || strings.Contains(raw, "returning"):
		return "continuing"
	case inProduction:
		return "in_production"
	case raw == "":
		return "unknown"
	}
	return "unknown"
}

func mapLibrary(d *seriesdetail.Detail) dto.LibraryStrip {
	// Story 374: EpisodesOnDisk + SizeOnDiskBytes come straight from
	// the cache row populated by the Sonarr sync writer (series
	// statistics). This decouples the LibraryStrip totals from the
	// episode_states branch — which can degrade silently (218 soft-
	// delete + missing resurrection) — and matches Sonarr's authoritative
	// counters. Total episode count + DominantQuality still derive from
	// the seasons/episodes branch because per-season totals and per-
	// episode quality strings are not cached on the row.
	lib := dto.LibraryStrip{
		Monitored:       d.CacheEntry.Monitored,
		MissingCount:    d.CacheEntry.MissingCount,
		EpisodesOnDisk:  d.CacheEntry.EpisodeFileCount,
		EpisodesAired:   d.CacheEntry.AiredEpisodeCount,
		SizeOnDiskBytes: d.CacheEntry.SizeOnDiskBytes,
	}
	var total int
	qualityCount := map[string]int{}
	for _, s := range d.Seasons {
		for _, e := range s.Episodes {
			total++
			if e.State != nil && e.State.HasFile {
				if e.State.Quality != nil && *e.State.Quality != "" {
					qualityCount[*e.State.Quality]++
				}
			}
		}
	}
	lib.EpisodesTotal = total
	// Dominant quality = the quality with the highest count.
	var dominant string
	highest := 0
	for q, n := range qualityCount {
		if n > highest {
			highest = n
			dominant = q
		}
	}
	lib.DominantQuality = dominant
	// Story 379: project the composer's in-progress pick onto the
	// LibraryStrip for the hero pill. nil when no record is downloading
	// OR Sonarr is unreachable (degraded[] already includes "sonarr").
	if d.InProgress != nil {
		lib.InProgress = &dto.InProgress{
			SeasonNumber:  d.InProgress.SeasonNumber,
			EpisodeNumber: d.InProgress.EpisodeNumber,
			Title:         d.InProgress.Title,
			Percent:       d.InProgress.Percent,
		}
	}
	return lib
}

func mapDownload(d *seriesdetail.Detail) *dto.DownloadChip {
	if d.Queue == nil {
		return nil
	}
	return &dto.DownloadChip{
		QueueID:      d.Queue.QueueID,
		EpisodeID:    int(d.Queue.SonarrEpisodeID),
		SeasonNumber: d.Queue.SeasonNumber,
		Title:        d.Queue.Title,
		Status:       d.Queue.Status,
		Protocol:     d.Queue.Protocol,
		DownloadID:   d.Queue.DownloadID,
	}
}

func mapOverview(d *seriesdetail.Detail) *dto.OverviewAside {
	overview := ""
	language := ""
	if d.Text != nil && d.Text.Overview != nil {
		overview = *d.Text.Overview
		language = d.Text.Language
	}
	keywords := []dto.TaxonomyChip{}
	for _, k := range d.Keywords {
		keywords = append(keywords, dto.TaxonomyChip{ID: k.ID, Name: k.Name, Language: k.Language})
	}
	var awards *string
	if d.Canon.OMDBAwards != nil && *d.Canon.OMDBAwards != "" && *d.Canon.OMDBAwards != "N/A" {
		v := *d.Canon.OMDBAwards
		awards = &v
	}
	// Suppress when truly empty.
	if overview == "" && len(keywords) == 0 && awards == nil {
		return nil
	}
	return &dto.OverviewAside{
		Overview: overview,
		Language: language,
		Keywords: keywords,
		Awards:   awards,
	}
}

func mapRecent(items []seriesdetail.RecentItem) []dto.RecentEvent {
	out := make([]dto.RecentEvent, 0, len(items))
	for _, it := range items {
		out = append(out, dto.RecentEvent{EventType: it.EventType, Subject: it.Subject, At: it.At})
	}
	return out
}

// mapSeasons projects the composer's SeasonDetail slice onto the DTO.
// Story 379 refactor: takes *Detail so it can read d.QueueRecords for
// the per-season downloading_count chip. Pure projection; no DB / IO.
func mapSeasons(d *seriesdetail.Detail) []dto.Season {
	if d == nil {
		return []dto.Season{}
	}
	out := make([]dto.Season, 0, len(d.Seasons))
	for _, s := range d.Seasons {
		ds := dto.Season{
			SeasonNumber: s.Canon.SeasonNumber,
			Name:         s.Canon.Name,
			Overview:     s.Canon.Overview,
			AirDate:      s.Canon.AirDate,
			PosterAsset:  s.Canon.PosterAsset,
			EpisodeCount: 0,
			Episodes:     make([]dto.Episode, 0, len(s.Episodes)),
		}
		if s.Canon.EpisodeCount != nil {
			ds.EpisodeCount = *s.Canon.EpisodeCount
		}
		for _, e := range s.Episodes {
			ep := dto.Episode{
				EpisodeNumber:  e.Canon.EpisodeNumber,
				AirDate:        e.Canon.AirDate,
				RuntimeMinutes: e.Canon.RuntimeMinutes,
				FinaleType:     e.Canon.FinaleType,
				StillAsset:     e.Canon.StillAsset,
			}
			if e.Canon.SonarrEpisodeID != nil {
				v := *e.Canon.SonarrEpisodeID
				ep.SonarrEpisodeID = &v
			}
			if e.Text != nil {
				ep.Title = e.Text.Title
				ep.TitleLanguage = e.Text.Language
				ep.Overview = e.Text.Overview
				ep.OverviewLanguage = e.Text.Language
			}
			if e.State != nil {
				ep.Monitored = e.State.Monitored
				ep.HasFile = e.State.HasFile
				ep.Quality = e.State.Quality
				ep.SizeBytes = e.State.SizeBytes
				ep.VideoCodec = e.State.VideoCodec
				ep.AudioCodec = e.State.AudioCodec
				ep.AudioChannels = e.State.AudioChannels
				ep.ReleaseGroup = e.State.ReleaseGroup
			}
			ds.Episodes = append(ds.Episodes, ep)
		}
		// Story 377: prefer the persisted Sonarr season.statistics
		// projection over walking episode_states. episode_states is
		// empty for fully-on-disk seasons skipped by
		// scan_skip_handled_seasons, which is the bug this fixes.
		// EpisodeCount on the dto is the "rendered episodes total" — we
		// prefer Stats.TotalEpisodeCount (includes unaired episodes) so
		// the accordion header "X/Y на диске" matches Sonarr.
		if s.Stats != nil {
			ds.Monitored = s.Stats.Monitored
			ds.OnDiskCount = s.Stats.EpisodeFileCount
			missing := s.Stats.AiredEpisodeCount - s.Stats.EpisodeFileCount
			if missing < 0 {
				missing = 0
			}
			ds.MissingCount = missing
			if s.Stats.TotalEpisodeCount > 0 {
				ds.EpisodeCount = s.Stats.TotalEpisodeCount
			}
		} else {
			var onDisk, missing int
			for _, e := range s.Episodes {
				if e.State != nil && e.State.HasFile {
					onDisk++
				} else {
					missing++
				}
			}
			ds.OnDiskCount = onDisk
			ds.MissingCount = missing
		}
		if ds.EpisodeCount == 0 {
			ds.EpisodeCount = len(s.Episodes)
		}
		// Story 379: per-season downloading chip. Count Sonarr queue
		// records with status=="downloading" whose seasonNumber matches.
		// 0 when no records OR Sonarr unreachable (degraded[]).
		for _, q := range d.QueueRecords {
			if q.SeasonNumber == s.Canon.SeasonNumber && q.Status == "downloading" {
				ds.DownloadingCount++
			}
		}
		out = append(out, ds)
	}
	return out
}

func mapCast(cast []seriesdetail.CastDetail) []dto.CastMember {
	out := make([]dto.CastMember, 0, len(cast))
	for _, c := range cast {
		m := dto.CastMember{
			PersonID:      c.Person.ID,
			TMDBPersonID:  c.Person.TMDBID,
			Name:          c.Person.Name,
			CharacterName: c.Credit.CharacterName,
			EpisodeCount:  c.Credit.EpisodeCount,
			ProfileAsset:  c.Person.ProfileAsset,
			CreditOrder:   c.Credit.CreditOrder,
		}
		out = append(out, m)
	}
	return out
}

func mapRecommendations(recs []seriesdetail.RecommendationDetail) []dto.Recommendation {
	out := make([]dto.Recommendation, 0, len(recs))
	for _, r := range recs {
		m := dto.Recommendation{
			SeriesID:       r.Series.ID,
			TMDBSeriesID:   r.Series.TMDBID,
			Title:          r.Series.Title,
			Year:           r.Series.Year,
			PosterAsset:    r.Series.PosterAsset,
			TMDBRating:     r.Series.TMDBRating,
			InLibrary:      r.InLibrary,
			InstanceName:   r.InstanceName,
			SonarrSeriesID: r.SonarrSeriesID,
		}
		out = append(out, m)
	}
	return out
}

func mapExternalLinks(xids map[string]string, canon series.Canon) dto.ExternalLinks {
	out := dto.ExternalLinks{
		Homepage: canon.Homepage,
	}
	// Prefer canon-projected ids; external_ids row dump fills any
	// gaps. The two paths should agree post-cutover.
	if canon.IMDBID != nil {
		v := *canon.IMDBID
		out.IMDbID = &v
	} else if v, ok := xids["imdb"]; ok && v != "" {
		s := domain.IMDBID(v)
		out.IMDbID = &s
	}
	if canon.TMDBID != nil {
		v := *canon.TMDBID
		out.TMDBID = &v
	} else if v, ok := xids["tmdb"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tm := domain.TMDBID(n)
			out.TMDBID = &tm
		}
	}
	if canon.TVDBID != nil {
		v := *canon.TVDBID
		out.TVDBID = &v
	} else if v, ok := xids["tvdb"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tv := domain.TVDBID(n)
			out.TVDBID = &tv
		}
	}
	return out
}

// sourceStringSlice projects []enrichment.Source → []string for
// the wire.
func sourceStringSlice(s []enrichment.Source) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		out = append(out, string(v))
	}
	return out
}
