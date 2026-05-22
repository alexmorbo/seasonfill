package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type InstancesHandler struct {
	checker *healthcheck.Checker
	clients map[string]ports.SonarrClient
	modes   map[string]string
	logger  *slog.Logger
}

// NewInstancesHandler — `clients`/`modes` nil-OK for back-compat with
// tests that only exercise List (Missing then 404s for every name).
func NewInstancesHandler(checker *healthcheck.Checker, clients map[string]ports.SonarrClient, modes map[string]string, logger *slog.Logger) *InstancesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &InstancesHandler{checker: checker, clients: clients, modes: modes, logger: logger}
}

// BuildModeMap — name->mode lookup; empty mode defaults to "auto".
func BuildModeMap(instances []config.SonarrInstance) map[string]string {
	out := make(map[string]string, len(instances))
	for _, inst := range instances {
		m := inst.Mode
		if m == "" {
			m = "auto"
		}
		out[inst.Name] = m
	}
	return out
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
	out := make([]dto.Instance, 0, len(snap))
	for _, s := range snap {
		out = append(out, snapshotToDTO(s, h.modes))
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
	client, ok := h.clients[name]
	if !ok {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}
	ctx := c.Request.Context()
	allSeries, err := client.ListSeries(ctx)
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
	c.JSON(http.StatusOK, dto.MissingSeriesList{Items: items, Total: len(items)})
}

func snapshotToDTO(s instance.Snapshot, modes map[string]string) dto.Instance {
	var lastCheckAt *time.Time
	if !s.LastCheckAt.IsZero() {
		t := s.LastCheckAt
		lastCheckAt = &t
	}
	mode := "auto"
	if modes != nil {
		if m, ok := modes[s.Name]; ok && m != "" {
			mode = m
		}
	}
	return dto.Instance{
		Name: s.Name, Mode: mode, Health: string(s.Health),
		LastCheckAt: lastCheckAt, LastError: s.LastError,
		TransitionsCount: s.TransitionsCount,
	}
}
