package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
)

// AuditHandler exposes the four read-only audit endpoints. Constructor
// takes port interfaces so tests can substitute fakes (though in-package
// tests use real GORM repos against in-memory SQLite for stronger
// coverage).
type AuditHandler struct {
	scans     ports.ScanRepository
	decisions ports.DecisionRepository
	grabs     ports.GrabRepository
}

func NewAuditHandler(scans ports.ScanRepository, decisions ports.DecisionRepository, grabs ports.GrabRepository) *AuditHandler {
	return &AuditHandler{scans: scans, decisions: decisions, grabs: grabs}
}

// stringPtrFromQuery returns a pointer to the trimmed query value, or
// nil if absent / empty. Used to populate the pointer-typed filter
// fields without repeated three-line `if` blocks.
func stringPtrFromQuery(c *gin.Context, name string) *string {
	v := c.Query(name)
	if v == "" {
		return nil
	}
	return &v
}

// --- JSON view structs (framework annotations live in the interface
// layer; domain and port types stay framework-free).

type scanView struct {
	ID              string     `json:"id"`
	Instance        string     `json:"instance"`
	Trigger         string     `json:"trigger"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Status          string     `json:"status"`
	SeriesScanned   int        `json:"series_scanned"`
	CandidatesFound int        `json:"candidates_found"`
	GrabsPerformed  int        `json:"grabs_performed"`
	GrabsFailed     int        `json:"grabs_failed"`
	ErrorsCount     int        `json:"errors_count"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	DryRun          bool       `json:"dry_run"`
}

func toScanView(r ports.ScanRecord) scanView {
	return scanView{
		ID:              r.ID.String(),
		Instance:        r.InstanceName,
		Trigger:         r.Trigger,
		CreatedAt:       r.CreatedAt,
		StartedAt:       r.StartedAt,
		FinishedAt:      r.FinishedAt,
		Status:          r.Status,
		SeriesScanned:   r.SeriesScanned,
		CandidatesFound: r.CandidatesFound,
		GrabsPerformed:  r.GrabsPerformed,
		GrabsFailed:     r.GrabsFailed,
		ErrorsCount:     r.ErrorsCount,
		ErrorMessage:    r.ErrorMessage,
		DryRun:          r.DryRun,
	}
}

type decisionView struct {
	ID              string    `json:"id"`
	ScanRunID       string    `json:"scan_run_id"`
	Instance        string    `json:"instance"`
	SeriesID        int       `json:"series_id"`
	SeriesTitle     string    `json:"series_title"`
	SeasonNumber    int       `json:"season_number"`
	Decision        string    `json:"decision"`
	Reason          string    `json:"reason"`
	MissingCount    int       `json:"missing_count"`
	ExistingCount   int       `json:"existing_count"`
	ReleasesFound   int       `json:"releases_found"`
	CandidatesCount int       `json:"candidates_count"`
	SelectedGUID    string    `json:"selected_guid,omitempty"`
	DryRunWouldGrab bool      `json:"dry_run_would_grab"`
	CreatedAt       time.Time `json:"created_at"`
}

func toDecisionView(d decision.Decision) decisionView {
	var selectedGUID string
	if d.Selected != nil {
		selectedGUID = d.Selected.Release.GUID
	}
	return decisionView{
		ID:              d.ID.String(),
		ScanRunID:       d.ScanRunID.String(),
		Instance:        d.InstanceName,
		SeriesID:        d.SeriesID,
		SeriesTitle:     d.SeriesTitle,
		SeasonNumber:    d.SeasonNumber,
		Decision:        string(d.Outcome),
		Reason:          string(d.Reason),
		MissingCount:    d.MissingCount,
		ExistingCount:   d.ExistingCount,
		ReleasesFound:   d.ReleasesFound,
		CandidatesCount: d.CandidatesCount,
		SelectedGUID:    selectedGUID,
		DryRunWouldGrab: d.DryRunWouldGrab,
		CreatedAt:       d.CreatedAt,
	}
}

type grabView struct {
	ID                string    `json:"id"`
	Instance          string    `json:"instance"`
	SeriesID          int       `json:"series_id"`
	SeriesTitle       string    `json:"series_title"`
	SeasonNumber      int       `json:"season_number"`
	ReleaseGUID       string    `json:"release_guid"`
	ReleaseTitle      string    `json:"release_title"`
	IndexerID         int       `json:"indexer_id"`
	IndexerName       string    `json:"indexer_name"`
	CustomFormatScore int       `json:"custom_format_score"`
	Quality           string    `json:"quality"`
	CoverageCount     int       `json:"coverage_count"`
	Status            string    `json:"status"`
	ErrorMessage      string    `json:"error_message,omitempty"`
	ScanRunID         string    `json:"scan_run_id"`
	Attempts          int       `json:"attempts"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func toGrabView(r grab.Record) grabView {
	return grabView{
		ID:                r.ID.String(),
		Instance:          r.InstanceName,
		SeriesID:          r.SeriesID,
		SeriesTitle:       r.SeriesTitle,
		SeasonNumber:      r.SeasonNumber,
		ReleaseGUID:       r.ReleaseGUID,
		ReleaseTitle:      r.ReleaseTitle,
		IndexerID:         r.IndexerID,
		IndexerName:       r.IndexerName,
		CustomFormatScore: r.CustomFormatScore,
		Quality:           r.Quality,
		CoverageCount:     r.CoverageCount,
		Status:            string(r.Status),
		ErrorMessage:      r.ErrorMessage,
		ScanRunID:         r.ScanRunID.String(),
		Attempts:          r.Attempts,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

// --- handlers -------------------------------------------------------------

// ListScans handles GET /api/v1/scans.
func (h *AuditHandler) ListScans(c *gin.Context) {
	limit, err := parseLimit(c)
	if handleQueryErr(c, err) {
		return
	}
	cursor, err := parseCursor(c)
	if handleQueryErr(c, err) {
		return
	}
	from, to, err := parseTimeRange(c)
	if handleQueryErr(c, err) {
		return
	}

	filter := ports.ScanFilter{
		From:     from,
		To:       to,
		Instance: stringPtrFromQuery(c, "instance"),
		Status:   stringPtrFromQuery(c, "status"),
	}
	recs, next, err := h.scans.List(c.Request.Context(), filter, ports.Pagination{Limit: limit, Cursor: cursor})
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]scanView, 0, len(recs))
	for _, r := range recs {
		out = append(out, toScanView(r))
	}
	writeListResponse(c, out, next)
}

// GetScan handles GET /api/v1/scans/:id.
func (h *AuditHandler) GetScan(c *gin.Context) {
	raw := c.Param("id")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid id")
		return
	}
	rec, err := h.scans.GetByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeError(c, http.StatusNotFound, "scan not found")
			return
		}
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, toScanView(rec))
}

// ListDecisions handles GET /api/v1/decisions.
func (h *AuditHandler) ListDecisions(c *gin.Context) {
	limit, err := parseLimit(c)
	if handleQueryErr(c, err) {
		return
	}
	cursor, err := parseCursor(c)
	if handleQueryErr(c, err) {
		return
	}
	from, to, err := parseTimeRange(c)
	if handleQueryErr(c, err) {
		return
	}
	seriesID, err := parseOptionalInt(c, "series_id")
	if handleQueryErr(c, err) {
		return
	}
	season, err := parseOptionalInt(c, "season_number")
	if handleQueryErr(c, err) {
		return
	}

	filter := ports.DecisionFilter{
		From:         from,
		To:           to,
		SeriesID:     seriesID,
		SeasonNumber: season,
		Instance:     stringPtrFromQuery(c, "instance"),
		Decision:     stringPtrFromQuery(c, "decision"),
	}
	if v := c.Query("scan_run_id"); v != "" {
		id, perr := uuid.Parse(v)
		if perr != nil {
			writeError(c, http.StatusBadRequest, "invalid scan_run_id")
			return
		}
		filter.ScanRunID = &id
	}
	recs, next, err := h.decisions.List(c.Request.Context(), filter, ports.Pagination{Limit: limit, Cursor: cursor})
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]decisionView, 0, len(recs))
	for _, d := range recs {
		out = append(out, toDecisionView(d))
	}
	writeListResponse(c, out, next)
}

// ListGrabs handles GET /api/v1/grabs.
func (h *AuditHandler) ListGrabs(c *gin.Context) {
	limit, err := parseLimit(c)
	if handleQueryErr(c, err) {
		return
	}
	cursor, err := parseCursor(c)
	if handleQueryErr(c, err) {
		return
	}
	from, to, err := parseTimeRange(c)
	if handleQueryErr(c, err) {
		return
	}
	seriesID, err := parseOptionalInt(c, "series_id")
	if handleQueryErr(c, err) {
		return
	}
	season, err := parseOptionalInt(c, "season_number")
	if handleQueryErr(c, err) {
		return
	}

	filter := ports.GrabFilter{
		From:         from,
		To:           to,
		SeriesID:     seriesID,
		SeasonNumber: season,
		Instance:     stringPtrFromQuery(c, "instance"),
		Status:       stringPtrFromQuery(c, "status"),
	}
	recs, next, err := h.grabs.List(c.Request.Context(), filter, ports.Pagination{Limit: limit, Cursor: cursor})
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]grabView, 0, len(recs))
	for _, r := range recs {
		out = append(out, toGrabView(r))
	}
	writeListResponse(c, out, next)
}
