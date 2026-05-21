package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/scan"
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

type scanResponseItem struct {
	ScanRunID    string    `json:"scan_run_id"`
	InstanceName string    `json:"instance"`
	Status       string    `json:"status"`
	Series       int       `json:"series_scanned"`
	Candidates   int       `json:"candidates_found"`
	Errors       int       `json:"errors"`
	Started      time.Time `json:"started_at"`
	Finished     time.Time `json:"finished_at"`
}

type scanRequest struct {
	Instance string `json:"instance"`
}

func (h *ScanHandler) Trigger(c *gin.Context) {
	var req scanRequest
	_ = c.ShouldBindJSON(&req)

	if req.Instance != "" {
		res, err := h.useCase.RunInstance(c.Request.Context(), req.Instance, scan.TriggerManual)
		if errors.Is(err, scan.ErrScanAlreadyRunning) {
			c.JSON(http.StatusConflict, gin.H{
				"error":    "scan already running",
				"instance": req.Instance,
				"code":     "SCAN_IN_PROGRESS",
			})
			return
		}
		if errors.Is(err, scan.ErrUnknownInstance) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":    "unknown instance",
				"instance": req.Instance,
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
		c.JSON(http.StatusAccepted, []scanResponseItem{toResponseItem(res)})
		return
	}

	results, err := h.useCase.Run(c.Request.Context(), scan.TriggerManual)
	if err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
		writeInternalError(c, h.logger, "scan_trigger_all_failed", err,
			slog.String("endpoint", "/api/v1/scan"),
		)
		return
	}
	out := make([]scanResponseItem, 0, len(results))
	for _, r := range results {
		out = append(out, toResponseItem(r))
	}
	c.JSON(http.StatusAccepted, out)
}

func toResponseItem(r scan.RunResult) scanResponseItem {
	return scanResponseItem{
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
