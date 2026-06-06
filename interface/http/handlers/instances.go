package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

const (
	// searchDefaultLimit — picker page size when ?limit is absent.
	// 30 fits comfortably above the dropdown fold without forcing the
	// operator to scroll past stale results during fast typing.
	searchDefaultLimit = 30
	// searchMaxLimit — hard ceiling. Picker UX caps at ~few dozen rows
	// before the typing-to-narrow loop becomes the better UX; 100 is
	// generous slack for power users with broad queries.
	searchMaxLimit = 100
)

type InstancesHandler struct {
	checker     *healthcheck.Checker
	reg         InstanceRegistry
	seriesCache ports.SeriesCacheRepository
	logger      *slog.Logger
}

// NewInstancesHandler — reg.Load may be nil (List then emits empty
// url/mode-defaulting-to-auto, Missing/SearchSeries 404 every name).
// seriesCache defaults to nil; production wires it via WithSeriesCache.
// Nil cache → Missing returns the same shape with empty TitleSlug /
// nil Year / nil PosterPath on every row (same as a cold cache).
func NewInstancesHandler(
	checker *healthcheck.Checker,
	reg InstanceRegistry,
	logger *slog.Logger,
) *InstancesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &InstancesHandler{checker: checker, reg: reg, logger: logger}
}

// WithSeriesCache wires the read-side of series_cache so Missing can
// enrich queue items (041g). Builder pattern keeps the constructor
// signature stable across the 10+ existing test call sites.
func (h *InstancesHandler) WithSeriesCache(repo ports.SeriesCacheRepository) *InstancesHandler {
	h.seriesCache = repo
	return h
}

// List returns the current health snapshot for every configured instance.
//
// @Summary     List Sonarr instance health
// @Description Latest snapshot from the in-memory checker.
// @Tags        instances
// @Produce     json
// @Success     200  {object}  dto.InstanceList
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances [get]
func (h *InstancesHandler) List(c *gin.Context) {
	snap := h.checker.Snapshot()
	instMap := h.reg.snapshot()
	out := make([]dto.Instance, 0, len(snap))
	for _, s := range snap {
		out = append(out, snapshotToDTO(s, instMap))
	}
	c.JSON(http.StatusOK, dto.InstanceList{Instances: out})
}

// Missing returns monitored series with aired episodes that have no
// file on disk, derived lazily from Sonarr's `series.statistics`.
// Works for both auto- and manual-mode instances (Q-010-4).
//
// @Summary     List missing-aired series for an instance
// @Description Monitored series whose aired episode count exceeds
// @Description the on-disk file count, with per-season breakdown.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.MissingSeriesList
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/missing [get]
func (h *InstancesHandler) Missing(c *gin.Context) {
	name := c.Param("name")
	inst, ok := h.reg.snapshot()[name]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}
	ctx := c.Request.Context()
	allSeries, err := inst.Client.ListSeries(ctx)
	if err != nil {
		// Upstream-auth failure surfaces as 502 — admin IS authenticated
		// to seasonfill; the Sonarr-side problem is a separate concern.
		if errors.Is(err, domain.ErrInstanceUnauthorized) {
			h.logger.WarnContext(ctx, "missing_upstream_unauthorized",
				slog.String("instance", name), slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
			return
		}
		h.logger.ErrorContext(ctx, "missing_list_series_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}
	items := make([]dto.MissingSeries, 0, len(allSeries))
	for _, s := range allSeries {
		if !s.Monitored {
			continue
		}
		total := s.Statistics.AiredMissing()
		if total == 0 {
			continue
		}
		seasons := make([]dto.MissingSeasonStat, 0, len(s.Seasons))
		for _, season := range s.Seasons {
			am := season.Statistics.AiredMissing()
			if am == 0 {
				continue
			}
			seasons = append(seasons, dto.MissingSeasonStat{
				SeasonNumber: season.Number, MissingAiredCount: am})
		}
		sort.Slice(seasons, func(i, j int) bool { return seasons[i].SeasonNumber < seasons[j].SeasonNumber })
		items = append(items, dto.MissingSeries{
			SeriesID: s.ID, Title: s.Title, Monitored: s.Monitored,
			TotalMissingAired: total, Seasons: seasons,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SeriesID < items[j].SeriesID })
	h.enrichMissingFromCache(ctx, name, items)
	c.JSON(http.StatusOK, dto.MissingSeriesList{Items: items, Total: len(items)})
}

// enrichMissingFromCache joins TitleSlug / Year / PosterPath from
// series_cache onto every item. ONE query per request — the in-memory
// map lookup is O(1) per item, no N+1. Repository errors WARN-log and
// the response continues unenriched (the queue page must NOT 5xx when
// the cache hiccups). nil h.seriesCache short-circuits to no-op.
func (h *InstancesHandler) enrichMissingFromCache(ctx context.Context, name string, items []dto.MissingSeries) {
	if h.seriesCache == nil || len(items) == 0 {
		return
	}
	entries, err := h.seriesCache.ListActiveByInstance(ctx, name)
	if err != nil {
		h.logger.WarnContext(ctx, "missing_cache_lookup_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
		return
	}
	byID := make(map[int]series.CacheEntry, len(entries))
	for _, e := range entries {
		byID[e.SonarrSeriesID] = e
	}
	for i := range items {
		e, ok := byID[items[i].SeriesID]
		if !ok {
			continue
		}
		items[i].TitleSlug = e.TitleSlug
		items[i].Year = e.Year
		items[i].PosterPath = e.PosterPath
	}
}

// snapshotToDTO reads URL and Mode from the live registry snapshot.
// instMap may be nil/empty; mode defaults to "auto" and url to "".
func snapshotToDTO(s instance.Snapshot, instMap map[string]scan.Instance) dto.Instance {
	var lastCheckAt *time.Time
	if !s.LastCheckAt.IsZero() {
		t := s.LastCheckAt
		lastCheckAt = &t
	}
	mode := "auto"
	var url string
	if inst, ok := instMap[s.Name]; ok {
		if m := inst.Config.Mode; m != "" {
			mode = m
		}
		url = inst.Config.URL // empty string is fine — UI falls back to ''
	}
	return dto.Instance{
		Name: s.Name, URL: url, Mode: mode, Health: string(s.Health),
		LastCheckAt: lastCheckAt, LastError: s.LastError,
		TransitionsCount: s.TransitionsCount,
	}
}

// SearchSeries returns matching monitored series for an instance,
// powering 013b's autocomplete picker. q is case-insensitive substring
// match on title; monitored filters server-side; limit clamps result
// length. Total reflects the count BEFORE limit so the UI can render
// "showing N of M". No cursor — autocomplete UX narrows by typing
// (Q-013a-1). No server-side cache (Q-013a-2).
//
// @Summary     Search series in a Sonarr instance
// @Description Title-substring search with monitored filter. Returns
// @Description a trimmed picker-specific DTO (series_id, title,
// @Description monitored, season_count, missing_aired_count). `total`
// @Description is the pre-limit count; clients narrow by typing more
// @Description rather than paginating.
// @Tags        instances
// @Produce     json
// @Param       name       path      string  true   "Instance name"
// @Param       q          query     string  false  "Title substring (case-insensitive)"
// @Param       monitored  query     string  false  "true | false | any (default any)"  Enums(true, false, any)
// @Param       limit      query     int     false  "1..100 (default 30)"
// @Success     200  {object}  dto.SeriesSearchList
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Failure     404  {object}  dto.ErrorResponse
// @Failure     502  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series [get]
func (h *InstancesHandler) SearchSeries(c *gin.Context) {
	name := c.Param("name")
	inst, ok := h.reg.snapshot()[name]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}

	limit, err := parseSearchLimit(c.Query("limit"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}
	monFilter, err := parseMonitoredFilter(c.Query("monitored"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))

	ctx := c.Request.Context()
	allSeries, err := inst.Client.ListSeries(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceUnauthorized) {
			h.logger.WarnContext(ctx, "search_upstream_unauthorized",
				slog.String("instance", name), slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
			return
		}
		h.logger.ErrorContext(ctx, "search_list_series_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	// Filter pass (q + monitored). Total counts post-filter, pre-limit
	// so 013b's UI can render "showing N of M".
	filtered := make([]series.Series, 0, len(allSeries))
	for _, s := range allSeries {
		if monFilter != nil && s.Monitored != *monFilter {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(s.Title), q) {
			continue
		}
		filtered = append(filtered, s)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return strings.ToLower(filtered[i].Title) < strings.ToLower(filtered[j].Title)
	})
	total := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	items := make([]dto.SeriesSearchItem, 0, len(filtered))
	for _, s := range filtered {
		items = append(items, toSeriesSearchItem(s))
	}
	c.JSON(http.StatusOK, dto.SeriesSearchList{Items: items, Total: total})
}

// parseSearchLimit clamps to [1, searchMaxLimit]; empty = default.
// Returns a wire-safe error string (no leaking internal types).
func parseSearchLimit(raw string) (int, error) {
	if raw == "" {
		return searchDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if n < 1 || n > searchMaxLimit {
		return 0, errors.New("limit must be between 1 and 100")
	}
	return n, nil
}

// parseMonitoredFilter returns nil for "any"/empty (no filter), or a
// *bool for true/false. Anything else is a 400. Kept lenient on case
// so the operator-typed `?monitored=True` doesn't surprise-fail.
func parseMonitoredFilter(raw string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "any":
		return nil, nil
	case "true":
		t := true
		return &t, nil
	case "false":
		f := false
		return &f, nil
	}
	return nil, errors.New("monitored must be one of: true, false, any")
}

// toSeriesSearchItem trims series.Series down to the picker DTO.
// SeasonCount is monitored-only (a picker filtering for "what could be
// scanned" should not count Specials or unmonitored seasons).
func toSeriesSearchItem(s series.Series) dto.SeriesSearchItem {
	monSeasons := 0
	for _, season := range s.Seasons {
		if season.Monitored {
			monSeasons++
		}
	}
	return dto.SeriesSearchItem{
		SeriesID: s.ID, Title: s.Title, Monitored: s.Monitored,
		SeasonCount: monSeasons, MissingAired: s.Statistics.AiredMissing(),
	}
}
