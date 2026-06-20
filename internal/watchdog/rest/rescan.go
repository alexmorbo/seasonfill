package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/rescan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
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
// @Description Asynchronously re-evaluates (instance, series, season).
// @Description Bypasses GUID cooldowns. Returns 202 immediately with a
// @Description fresh scan_run_id; the goroutine writes the new decision
// @Description and marks the original superseded. 409 if the original
// @Description already produced a grab_records row or if another scan
// @Description is running on the same instance.
// @Tags        decisions
// @Produce     json
// @Param       id   path      string                    true  "Decision UUID"
// @Success     202  {array}   dto.ScanTriggerItem
// @Failure     400  {object}  dto.ErrorResponse         "invalid id"
// @Failure     404  {object}  dto.ErrorResponse         "decision or instance not found"
// @Failure     409  {object}  dto.ScanConflictResponse  "scan already running on instance"
// @Failure     409  {object}  dto.ErrorResponse         "decision already superseded or already executed"
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /decisions/{id}/rescan [post]
func (h *RescanHandler) ByDecision(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		handlers.WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}
	res, err := h.rescanUC.Start(ctx, rescan.Input{DecisionID: id})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrNotFound):
			_ = c.Error(err)
		case errors.Is(err, rescan.ErrAlreadySuperseded):
			c.JSON(http.StatusConflict, dto.ErrorResponse{
				Error: "decision already superseded; rescan the successor instead"})
		case errors.Is(err, rescan.ErrAlreadyExecuted):
			c.JSON(http.StatusConflict, dto.ErrorResponse{
				Error: "decision already executed; create a new scan instead"})
		case errors.Is(err, scan.ErrScanAlreadyRunning):
			c.JSON(http.StatusConflict, dto.ScanConflictResponse{
				Error:    "scan already running",
				Instance: res.Instance,
				Code:     "SCAN_IN_PROGRESS",
			})
		case strings.HasPrefix(err.Error(), "unknown instance: "):
			handlers.WriteError(c, http.StatusNotFound, err.Error())
		default:
			handlers.WriteInternalError(c, h.logger, "rescan_failed", err,
				slog.String("decision_id", id.String()))
		}
		return
	}
	c.JSON(http.StatusAccepted, []dto.ScanTriggerItem{{
		ScanRunID:    res.ScanRunID.String(),
		InstanceName: res.Instance,
		Status:       res.Status,
		Started:      res.Started,
	}})
}
