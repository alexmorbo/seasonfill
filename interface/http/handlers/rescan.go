package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

type RescanHandler struct {
	rescanUC *rescan.UseCase
	logger   *slog.Logger
}

func NewRescanHandler(uc *rescan.UseCase, logger *slog.Logger) *RescanHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RescanHandler{rescanUC: uc, logger: logger}
}

// ByDecision handles POST /api/v1/decisions/{id}/rescan.
//
// @Summary     Rescan a single decision
// @Description Re-evaluates (instance, series, season). Bypasses GUID
// @Description cooldowns. New decision shares scan_run_id; original is
// @Description marked superseded. 409 if original already produced a
// @Description grab_records row.
// @Tags        decisions
// @Produce     json
// @Param       id   path      string  true  "Decision UUID"
// @Success     200  {object}  dto.Decision
// @Failure     400,404,409,500,502  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /decisions/{id}/rescan [post]
func (h *RescanHandler) ByDecision(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid id")
		return
	}
	out, err := h.rescanUC.Execute(ctx, rescan.Input{DecisionID: id})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrNotFound):
			writeError(c, http.StatusNotFound, "decision not found")
		case errors.Is(err, rescan.ErrAlreadySuperseded):
			c.JSON(http.StatusConflict, dto.ErrorResponse{
				Error: "decision already superseded; rescan the successor instead"})
		case errors.Is(err, rescan.ErrAlreadyExecuted):
			c.JSON(http.StatusConflict, dto.ErrorResponse{
				Error: "decision already executed; create a new scan instead"})
		case errors.Is(err, domain.ErrInstanceUnauthorized):
			h.logger.WarnContext(ctx, "rescan_upstream_unauthorized",
				slog.String("decision_id", id.String()), slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
		case errors.Is(err, domain.ErrInstanceNetwork):
			h.logger.WarnContext(ctx, "rescan_upstream_network_error",
				slog.String("decision_id", id.String()), slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		case strings.HasPrefix(err.Error(), "unknown instance: "):
			writeError(c, http.StatusNotFound, err.Error())
		default:
			writeInternalError(c, h.logger, "rescan_failed", err,
				slog.String("decision_id", id.String()))
		}
		return
	}
	c.JSON(http.StatusOK, toDecisionDTO(out.NewDecision))
}
