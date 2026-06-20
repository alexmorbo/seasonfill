package rest

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
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
// @Description Asynchronously schedules a scan across all configured
// @Description instances or the named one. Returns 202 immediately
// @Description with status="running"; clients poll /scans/{id} for
// @Description live progress (series_scanned increments mid-scan).
// @Description Optional `series_ids` narrows a per-instance scan;
// @Description unknown IDs are silently dropped with a WARN.
// @Tags        scans
// @Accept      json
// @Produce     json
// @Param       body  body      dto.ScanTriggerRequest  false  "Optional instance + series filter"
// @Success     202   {array}   dto.ScanTriggerItem
// @Failure     404   {object}  dto.ScanNotFoundResponse
// @Failure     409   {object}  dto.ScanConflictResponse  "SCAN_IN_PROGRESS"
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /scan [post]
func (h *ScanHandler) Trigger(c *gin.Context) {
	var req dto.ScanTriggerRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		// Story 121b §E: empty body (io.EOF) is the documented
		// "scan all instances" path and must not 400. Any other parse
		// failure — malformed JSON, type mismatch on an optional
		// field — surfaces as 400 so the operator sees the typo
		// instead of silently triggering a full-instance scan.
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}

	if req.Instance != "" {
		res, err := h.useCase.StartInstanceWithDryRun(c.Request.Context(), string(req.Instance), scan.TriggerManual, req.DryRun, req.SeriesIDs...)
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
				slog.String("instance", string(req.Instance)),
			)
			return
		}
		c.JSON(http.StatusAccepted, []dto.ScanTriggerItem{toScanTriggerItem(res)})
		return
	}

	results, err := h.useCase.Start(c.Request.Context(), scan.TriggerManual)
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

// Cancel handles POST /api/v1/scans/:id/cancel.
//
// @Summary     Cancel a running scan
// @Description Signals cancellation of the named scan run. The goroutine
// @Description observes the signal at the next ctx.Err() checkpoint and
// @Description finalises with status="cancelled". Already-collected
// @Description decisions are kept; already-issued grabs are NOT undone.
// @Tags        scans
// @Produce     json
// @Param       id   path      string  true  "Scan run UUID"
// @Success     202  {object}  dto.OKResponse
// @Failure     400  {object}  dto.ErrorResponse  "invalid id"
// @Failure     404  {object}  dto.ErrorResponse  "scan not running"
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /scans/{id}/cancel [post]
func (h *ScanHandler) Cancel(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid id")
		return
	}
	if cerr := h.useCase.Cancel(c.Request.Context(), id); cerr != nil {
		if errors.Is(cerr, scan.ErrScanNotRunning) {
			writeError(c, http.StatusNotFound, "scan not running")
			return
		}
		writeInternalError(c, h.logger, "scan_cancel_failed", cerr,
			slog.String("scan_id", id.String()))
		return
	}
	c.JSON(http.StatusAccepted, dto.OKResponse{OK: true})
}
