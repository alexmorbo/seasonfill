package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

type ScanHandler struct {
	useCase *scan.UseCase
	logger  *slog.Logger
}

// NewScanHandler wires the manual-scan trigger endpoint with the
// scan use case and a logger. A nil logger falls back to
// slog.Default() (see writeInternalError); production wiring always
// passes a real logger.
func NewScanHandler(uc *scan.UseCase, logger *slog.Logger) *ScanHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScanHandler{useCase: uc, logger: logger}
}

// Trigger handles POST /api/v1/scan.
//
// @Summary     Trigger a manual scan
// @Description Schedules a scan across all configured instances or the
// @Description named one. Returns 202; clients poll /scans/{id}.
// @Tags        scans
// @Accept      json
// @Produce     json
// @Param       body  body      dto.ScanTriggerRequest  false  "Optional instance selector"
// @Success     202   {array}   dto.ScanTriggerItem
// @Failure     404   {object}  dto.ScanNotFoundResponse
// @Failure     409   {object}  dto.ScanConflictResponse  "SCAN_IN_PROGRESS"
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /scan [post]
func (h *ScanHandler) Trigger(c *gin.Context) {
	var req dto.ScanTriggerRequest
	_ = c.ShouldBindJSON(&req)

	if req.Instance != "" {
		res, err := h.useCase.RunInstance(c.Request.Context(), req.Instance, scan.TriggerManual)
		if errors.Is(err, scan.ErrScanAlreadyRunning) {
			c.JSON(http.StatusConflict, dto.ScanConflictResponse{
				Error:    "scan already running",
				Instance: req.Instance,
				Code:     "SCAN_IN_PROGRESS",
			})
			return
		}
		if errors.Is(err, scan.ErrUnknownInstance) {
			c.JSON(http.StatusNotFound, dto.ScanNotFoundResponse{
				Error:    "unknown instance",
				Instance: req.Instance,
			})
			return
		}
		if err != nil {
			writeInternalError(c, h.logger, "scan_trigger_instance_failed", err,
				slog.String("endpoint", "/api/v1/scan"),
				slog.String("instance", req.Instance),
			)
			return
		}
		c.JSON(http.StatusAccepted, []dto.ScanTriggerItem{toScanTriggerItem(res)})
		return
	}

	results, err := h.useCase.Run(c.Request.Context(), scan.TriggerManual)
	if err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
		writeInternalError(c, h.logger, "scan_trigger_all_failed", err,
			slog.String("endpoint", "/api/v1/scan"),
		)
		return
	}
	out := make([]dto.ScanTriggerItem, 0, len(results))
	for _, r := range results {
		out = append(out, toScanTriggerItem(r))
	}
	c.JSON(http.StatusAccepted, out)
}

func toScanTriggerItem(r scan.RunResult) dto.ScanTriggerItem {
	return dto.ScanTriggerItem{
		ScanRunID:    r.ScanRunID.String(),
		InstanceName: r.InstanceName,
		Status:       r.Status,
		Series:       r.Series,
		Candidates:   r.Candidates,
		Errors:       r.Errors,
		Started:      r.Started,
		Finished:     r.Finished,
	}
}
