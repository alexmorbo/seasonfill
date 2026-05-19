package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/scan"
)

type ScanHandler struct {
	useCase *scan.UseCase
}

func NewScanHandler(uc *scan.UseCase) *ScanHandler {
	return &ScanHandler{useCase: uc}
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
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, []scanResponseItem{toResponseItem(res)})
		return
	}

	results, err := h.useCase.Run(c.Request.Context(), scan.TriggerManual)
	if err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
