package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

const monitorSeasonBodyLimit = 1 << 10

// MonitorSeasonExecutor is the narrow use-case surface the handler needs. The
// concrete *seriesdetail.MonitorSeasonUseCase satisfies it; tests inject a fake.
type MonitorSeasonExecutor interface {
	Execute(ctx context.Context, instanceName domain.InstanceName, seriesID domain.SeriesID, seasonNumber int, search bool) (seriesdetail.MonitorSeasonResult, error)
}

type monitorSeasonRequest struct {
	Search *bool `json:"search"`
}

type monitorSeasonResponse struct {
	Instance     string `json:"instance"`
	SeriesID     int    `json:"series_id"`
	SeasonNumber int    `json:"season_number"`
	Monitored    bool   `json:"monitored"`
	Searched     bool   `json:"searched"`
}

// MonitorSeasonHandler exposes POST
// /api/v1/instances/:name/series/:id/seasons/:season/monitor.
type MonitorSeasonHandler struct {
	uc     MonitorSeasonExecutor
	logger *slog.Logger
}

// NewMonitorSeasonHandler constructs the handler. logger nil-OK.
func NewMonitorSeasonHandler(uc MonitorSeasonExecutor, logger *slog.Logger) *MonitorSeasonHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &MonitorSeasonHandler{uc: uc, logger: logger}
}

// Post handles POST /api/v1/instances/:name/series/:id/seasons/:season/monitor.
//
// @Summary     Monitor a season in Sonarr and optionally trigger a search
// @Description Flips the season's monitored flag on the named instance's Sonarr
// @Description and, unless {"search": false} is posted, triggers a SeasonSearch.
// @Tags        series
// @Accept      json
// @Produce     json
// @Param       name    path  string  true   "Sonarr instance name"
// @Param       id      path  int     true   "Canonical series.id"
// @Param       season  path  int     true   "Season number"
// @Param       body    body  object  false  "{\"search\": bool} (defaults to true)"
// @Success     200 {object} monitorSeasonResponse
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Failure     502 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series/{id}/seasons/{season}/monitor [post]
func (h *MonitorSeasonHandler) Post(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "instance name required"})
		return
	}
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seasonNumber, err := strconv.Atoi(c.Param("season"))
	if err != nil || seasonNumber < 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid season number"})
		return
	}

	search := true
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, monitorSeasonBodyLimit)
	var body monitorSeasonRequest
	if derr := json.NewDecoder(c.Request.Body).Decode(&body); derr != nil && !errors.Is(derr, io.EOF) {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "malformed body"})
		return
	}
	if body.Search != nil {
		search = *body.Search
	}

	if h.uc == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "monitor season handler not wired"})
		return
	}
	res, err := h.uc.Execute(c.Request.Context(), domain.InstanceName(name), domain.SeriesID(parsedID), seasonNumber, search)
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	c.JSON(http.StatusOK, monitorSeasonResponse{
		Instance:     name,
		SeriesID:     parsedID,
		SeasonNumber: res.SeasonNumber,
		Monitored:    res.Monitored,
		Searched:     res.Searched,
	})
}
