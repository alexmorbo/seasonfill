package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// WatchdogSeasonsLister is the narrow slice of
// repositories.WatchdogSeasonsRepository the handler needs for the
// `/watchdog/seasons` page. Production is satisfied directly by
// *repositories.WatchdogSeasonsRepository.
type WatchdogSeasonsLister interface {
	ListSeasons(ctx context.Context, f repositories.WatchdogSeasonsFilter,
		limit int, cur *repositories.WatchdogSeasonsCursor, now time.Time,
	) ([]repositories.WatchdogSeasonRow, *repositories.WatchdogSeasonsCursor, error)
}

// WatchdogSeasonsSeriesLister is the narrow slice for the per-series
// drill endpoint. Production: *repositories.WatchdogSeasonsRepository.
type WatchdogSeasonsSeriesLister interface {
	SeasonsForSeries(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, now time.Time) ([]repositories.WatchdogSeasonRow, error)
	SeasonStatsFromDecisions(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID) (map[int]repositories.WatchdogSeasonStats, error)
	RecentDecisionsBySeason(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, perSeason int) (map[int][]repositories.RecentDecisionRow, error)
	RecentGrabsBySeason(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, perSeason int) (map[int][]repositories.RecentGrabRow, error)
}

// Limits for the `/watchdog/seasons` page. The defaults match the
// other audit list handlers; the hard cap (500) is the task-spec
// requirement.
const (
	watchdogSeasonsDefaultLimit = 100
	watchdogSeasonsMaxLimit     = 500
	watchdogSeriesRecentCap     = 20
)

// WatchdogSeasonsHandler serves the read-side aggregate endpoints
// introduced in Story 098a. Both handlers join the watchdog source-of-
// truth tables (origin_releases + cooldowns + regrab_no_better_counter
// + watchdog_blacklist) with series_cache for display strings, so the
// SPA can render the seasons-being-monitored page without a separate
// per-row fetch.
type WatchdogSeasonsHandler struct {
	repo     WatchdogSeasonsLister
	series   WatchdogSeasonsSeriesLister
	settings regrab.SettingsLookup
	logger   *slog.Logger
	now      func() time.Time
}

// NewWatchdogSeasonsHandler wires the handler. logger=nil →
// slog.Default. settings is optional: when nil, the per-season
// NoBetterCounter.Max field falls back to zero (it's only used to
// render the n/N progress and is non-essential for correctness).
func NewWatchdogSeasonsHandler(
	repo WatchdogSeasonsLister,
	series WatchdogSeasonsSeriesLister,
	settings regrab.SettingsLookup,
	logger *slog.Logger,
) *WatchdogSeasonsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WatchdogSeasonsHandler{
		repo:     repo,
		series:   series,
		settings: settings,
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// WithClock pins the time source. Tests only.
func (h *WatchdogSeasonsHandler) WithClock(f func() time.Time) *WatchdogSeasonsHandler {
	if f != nil {
		h.now = f
	}
	return h
}

// List handles GET /api/v1/watchdog/seasons.
//
// @Summary     List watchdog-tracked seasons
// @Description Aggregate read view driven by origin_releases.
// @Tags        watchdog
// @Produce     json
// @Param       instance         query  string  false  "Filter by instance name"
// @Param       q                query  string  false  "Search series title (LIKE %q%)"
// @Param       cooldown_only    query  bool    false  "Only seasons with an active cooldown"
// @Param       blacklisted_only query  bool    false  "Only seasons with an active blacklist row"
// @Param       limit            query  int     false  "Page size (default 100, max 500)"
// @Param       cursor           query  string  false  "Opaque next_cursor"
// @Success     200  {object}  dto.WatchdogSeasonsList
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /watchdog/seasons [get]
func (h *WatchdogSeasonsHandler) List(c *gin.Context) {
	ctx := c.Request.Context()

	limit, err := parseSeasonsLimit(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	var cur *repositories.WatchdogSeasonsCursor
	if raw := c.Query("cursor"); raw != "" {
		parsed, perr := decodeSeasonsCursor(raw)
		if perr != nil {
			writeError(c, http.StatusBadRequest, "invalid cursor")
			return
		}
		cur = parsed
	}

	filter := repositories.WatchdogSeasonsFilter{
		Instance:        domain.InstanceName(strings.TrimSpace(c.Query("instance"))),
		Q:               strings.TrimSpace(c.Query("q")),
		CooldownOnly:    boolQuery(c, "cooldown_only"),
		BlacklistedOnly: boolQuery(c, "blacklisted_only"),
	}

	rows, next, err := h.repo.ListSeasons(ctx, filter, limit, cur, h.now())
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_seasons_list_failed", err,
			slog.String("instance", string(filter.Instance)))
		return
	}

	out := dto.WatchdogSeasonsList{Items: make([]dto.WatchdogSeason, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, h.toSeasonDTO(ctx, row))
	}
	if next != nil {
		out.NextCursor = encodeSeasonsCursor(*next)
	}
	c.JSON(http.StatusOK, out)
}

// Series handles GET /api/v1/watchdog/series/:instance/:id.
//
// @Summary     Watchdog detail for one series
// @Description Per-season aggregate plus recent decisions + grabs.
// @Tags        watchdog
// @Produce     json
// @Param       instance  path  string  true  "Instance name"
// @Param       id        path  int     true  "Sonarr series id"
// @Success     200  {object}  dto.WatchdogSeriesDetail
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /watchdog/series/{instance}/{id} [get]
func (h *WatchdogSeasonsHandler) Series(c *gin.Context) {
	ctx := c.Request.Context()

	instanceRaw := strings.TrimSpace(c.Param("instance"))
	rawID := c.Param("id")
	if instanceRaw == "" {
		writeError(c, http.StatusBadRequest, "instance required")
		return
	}
	instance := domain.InstanceName(instanceRaw)
	parsedID, err := strconv.Atoi(rawID)
	if err != nil || parsedID <= 0 {
		writeError(c, http.StatusBadRequest, "invalid series id")
		return
	}
	seriesID := domain.SonarrSeriesID(parsedID)

	rows, err := h.series.SeasonsForSeries(ctx, instance, seriesID, h.now())
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_series_load_failed", err,
			slog.String("instance", instanceRaw),
			slog.Int("series_id", int(seriesID)))
		return
	}
	stats, err := h.series.SeasonStatsFromDecisions(ctx, instance, seriesID)
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_series_stats_failed", err,
			slog.String("instance", instanceRaw),
			slog.Int("series_id", int(seriesID)))
		return
	}
	decisionsBySeason, err := h.series.RecentDecisionsBySeason(ctx, instance, seriesID, watchdogSeriesRecentCap)
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_series_decisions_failed", err,
			slog.String("instance", instanceRaw),
			slog.Int("series_id", int(seriesID)))
		return
	}
	grabsBySeason, err := h.series.RecentGrabsBySeason(ctx, instance, seriesID, watchdogSeriesRecentCap)
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_series_grabs_failed", err,
			slog.String("instance", instanceRaw),
			slog.Int("series_id", int(seriesID)))
		return
	}

	out := dto.WatchdogSeriesDetail{
		Instance: instance,
		SeriesID: seriesID,
		Seasons:  make([]dto.WatchdogSeriesSeason, 0, len(rows)),
	}
	if len(rows) > 0 {
		out.SeriesTitle = rows[0].SeriesTitle
		out.Monitored = rows[0].Monitored
	}

	noBetterMax := h.noBetterMaxFor(ctx, instance)
	for _, row := range rows {
		seasonGrabs := grabsBySeason[row.SeasonNumber]
		season := dto.WatchdogSeriesSeason{
			SeasonNumber:    row.SeasonNumber,
			Origin:          originDTO(row),
			Cooldown:        cooldownDTO(row),
			NoBetterCounter: noBetterDTO(row, noBetterMax),
			Blacklist:       blacklistDTO(row),
			RecentDecisions: toRecentDecisionDTOs(decisionsBySeason[row.SeasonNumber]),
			RecentGrabs:     toRecentGrabDTOs(seasonGrabs),
		}
		if season.Origin != nil {
			season.Origin.TorrentHash = firstNonEmptyHash(seasonGrabs)
		}
		if s, ok := stats[row.SeasonNumber]; ok {
			missing := s.AiredEpisodes - s.ExistingEpisodes
			if missing < 0 {
				missing = 0
			}
			season.Stats = dto.WatchdogSeriesSeasonStats{
				AiredEpisodeCount: s.AiredEpisodes,
				EpisodeFileCount:  s.ExistingEpisodes,
				MissingAiredCount: missing,
			}
		}
		out.Seasons = append(out.Seasons, season)
	}
	c.JSON(http.StatusOK, out)
}

// toSeasonDTO is the per-row mapper for the seasons list. Inlined as
// a method so it can reach SettingsLookup for the noBetterMax field
// without threading it through repo rows.
func (h *WatchdogSeasonsHandler) toSeasonDTO(ctx context.Context, row repositories.WatchdogSeasonRow) dto.WatchdogSeason {
	item := dto.WatchdogSeason{
		Instance:          row.InstanceName,
		SeriesID:          row.SeriesID,
		SeriesTitle:       row.SeriesTitle,
		SeasonNumber:      row.SeasonNumber,
		Monitored:         row.Monitored,
		Origin:            originDTO(row),
		Cooldown:          cooldownDTO(row),
		NoBetterCounter:   noBetterDTO(row, h.noBetterMaxFor(ctx, row.InstanceName)),
		Blacklist:         blacklistDTO(row),
		MissingAiredCount: row.MissingAiredCount,
		LastAiredAt:       row.LastAiredAt,
	}
	return item
}

// noBetterMaxFor returns the per-instance MaxConsecutiveNoBetter
// setting, or zero when the settings lookup is unwired or the
// instance has no row. Cached per-call would be marginally faster on
// the list endpoint but the lookup is in-memory in production.
func (h *WatchdogSeasonsHandler) noBetterMaxFor(ctx context.Context, instance domain.InstanceName) int {
	if h.settings == nil {
		return 0
	}
	s, err := h.settings.Lookup(ctx, instance)
	if err != nil {
		if !errors.Is(err, ports.ErrNotFound) {
			h.logger.DebugContext(ctx, "watchdog_seasons_settings_lookup_failed",
				slog.String("instance", string(instance)),
				slog.String("error", err.Error()))
		}
		return 0
	}
	return s.MaxConsecutiveNoBetter
}

func originDTO(row repositories.WatchdogSeasonRow) *dto.WatchdogSeasonOrigin {
	if row.OriginGUID == "" && row.OriginFirstSeenAt.IsZero() && row.OriginLastSeenAt.IsZero() {
		// Row was synthesised from decisions only (no origin_releases
		// row exists). Surface origin = null per the wire contract.
		return nil
	}
	return &dto.WatchdogSeasonOrigin{
		Indexer:     row.OriginIndexerName,
		FirstSeenAt: row.OriginFirstSeenAt,
		LastSeenAt:  row.OriginLastSeenAt,
		LastUsedAt:  row.OriginLastUsedAt,
		GUID:        row.OriginGUID,
	}
}

func cooldownDTO(row repositories.WatchdogSeasonRow) *dto.WatchdogSeasonCooldown {
	if row.Cooldown == nil {
		return nil
	}
	return &dto.WatchdogSeasonCooldown{
		ExpiresAt: row.Cooldown.ExpiresAt,
		Reason:    row.Cooldown.Reason,
	}
}

func noBetterDTO(row repositories.WatchdogSeasonRow, max int) *dto.WatchdogSeasonNoBetter {
	if row.NoBetterCounter == nil {
		return nil
	}
	return &dto.WatchdogSeasonNoBetter{
		Consecutive: row.NoBetterCounter.Consecutive,
		Max:         max,
		LastSeenAt:  row.NoBetterCounter.LastSeenAt,
	}
}

func blacklistDTO(row repositories.WatchdogSeasonRow) *dto.WatchdogSeasonBlacklist {
	if row.Blacklist == nil {
		return nil
	}
	return &dto.WatchdogSeasonBlacklist{
		Reason:    string(row.Blacklist.Reason),
		ExpiresAt: row.Blacklist.ExpiresAt,
	}
}

func toRecentDecisionDTOs(rows []repositories.RecentDecisionRow) []dto.WatchdogSeriesRecentDecision {
	out := make([]dto.WatchdogSeriesRecentDecision, 0, len(rows))
	for _, r := range rows {
		out = append(out, dto.WatchdogSeriesRecentDecision{
			ID:        r.ID,
			ScanRunID: r.ScanRunID,
			Decision:  r.Decision,
			Reason:    r.Reason,
			CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func toRecentGrabDTOs(rows []repositories.RecentGrabRow) []dto.WatchdogSeriesRecentGrab {
	out := make([]dto.WatchdogSeriesRecentGrab, 0, len(rows))
	for _, r := range rows {
		out = append(out, dto.WatchdogSeriesRecentGrab{
			ID:           r.ID,
			ReleaseTitle: r.ReleaseTitle,
			Status:       r.Status,
			ReplayOfID:   r.ReplayOfID,
			CreatedAt:    r.CreatedAt,
		})
	}
	return out
}

// firstNonEmptyHash returns the torrent_hash of the most recent grab row
// that has one. Returns "" when no row has a non-nil hash. Rows are
// already sorted most-recent-first by RecentGrabsBySeason.
func firstNonEmptyHash(rows []repositories.RecentGrabRow) string {
	for _, r := range rows {
		if r.TorrentHash != nil && *r.TorrentHash != "" {
			return *r.TorrentHash
		}
	}
	return ""
}

// parseSeasonsLimit mirrors parseLimit but with the seasons-specific
// caps (default 100, max 500) called out in the story.
func parseSeasonsLimit(c *gin.Context) (int, error) {
	raw := c.Query("limit")
	if raw == "" {
		return watchdogSeasonsDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > watchdogSeasonsMaxLimit {
		return 0, errors.New("invalid limit")
	}
	return n, nil
}

func boolQuery(c *gin.Context, name string) bool {
	raw := strings.TrimSpace(strings.ToLower(c.Query(name)))
	if raw == "" {
		return false
	}
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// encodeSeasonsCursor packs the keyset tuple into a URL-safe base64
// string. The decoder rejects any payload with a different number of
// segments so a hand-crafted cursor can't trip the keyset predicate.
func encodeSeasonsCursor(c repositories.WatchdogSeasonsCursor) string {
	raw := string(c.InstanceName) + "\x00" + strconv.Itoa(int(c.SeriesID)) + "\x00" + strconv.Itoa(c.SeasonNumber)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeSeasonsCursor(s string) (*repositories.WatchdogSeasonsCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return nil, errors.New("watchdog seasons cursor: wrong segment count")
	}
	seriesID, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, err
	}
	season, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, err
	}
	return &repositories.WatchdogSeasonsCursor{
		InstanceName: domain.InstanceName(parts[0]),
		SeriesID:     domain.SonarrSeriesID(seriesID),
		SeasonNumber: season,
	}, nil
}
