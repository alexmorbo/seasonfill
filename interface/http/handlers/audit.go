package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// AuditHandler exposes the four read-only audit endpoints. Constructor
// takes port interfaces so tests can substitute fakes (though in-package
// tests use real GORM repos against in-memory SQLite for stronger
// coverage).
type AuditHandler struct {
	scans     ports.ScanRepository
	decisions ports.DecisionRepository
	grabs     ports.GrabRepository
	logger    *slog.Logger
}

// NewAuditHandler wires the audit endpoints with their backing repos
// and a logger. A nil logger falls back to slog.Default() (see
// writeInternalError); production wiring always passes a real logger.
func NewAuditHandler(scans ports.ScanRepository, decisions ports.DecisionRepository, grabs ports.GrabRepository, logger *slog.Logger) *AuditHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuditHandler{scans: scans, decisions: decisions, grabs: grabs, logger: logger}
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

// --- DTO mapping helpers (DTOs live in interface/http/dto; domain and
// port types stay framework-free).

func toScanDTO(r ports.ScanRecord) dto.Scan {
	return dto.Scan{
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

func toDecisionDTO(d decision.Decision) dto.Decision {
	var selectedGUID string
	if d.Selected != nil {
		selectedGUID = d.Selected.Release.GUID
	}
	return dto.Decision{
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

func toGrabDTO(r grab.Record) dto.Grab {
	return dto.Grab{
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
//
// @Summary     List scans
// @Description Keyset-paginated, newest first.
// @Tags        scans
// @Produce     json
// @Param       limit     query  int     false  "Page size (default 50, max 200)"
// @Param       cursor    query  string  false  "Opaque next_cursor"
// @Param       instance  query  string  false  "Filter by instance"
// @Param       status    query  string  false  "Filter by status"  Enums(running,completed,failed,aborted)
// @Param       from      query  string  false  "RFC3339 lower bound"
// @Param       to        query  string  false  "RFC3339 upper bound (exclusive)"
// @Success     200  {object}  dto.ScanList
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /scans [get]
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
		writeInternalError(c, h.logger, "audit_list_scans_failed", err,
			slog.String("endpoint", "/api/v1/scans"),
		)
		return
	}
	out := make([]dto.Scan, 0, len(recs))
	for _, r := range recs {
		out = append(out, toScanDTO(r))
	}
	writeListResponse(c, out, next)
}

// GetScan handles GET /api/v1/scans/:id.
//
// @Summary     Get scan by ID
// @Tags        scans
// @Produce     json
// @Param       id    path     string  true  "Scan run UUID"
// @Success     200   {object}  dto.Scan
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /scans/{id} [get]
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
		writeInternalError(c, h.logger, "audit_get_scan_failed", err,
			slog.String("endpoint", "/api/v1/scans/:id"),
			slog.String("scan_id", id.String()),
		)
		return
	}
	c.JSON(http.StatusOK, toScanDTO(rec))
}

// ListDecisions handles GET /api/v1/decisions.
//
// @Summary     List decisions
// @Tags        decisions
// @Produce     json
// @Param       limit          query  int     false  "Page size (default 50, max 200)"
// @Param       cursor         query  string  false  "Opaque next_cursor"
// @Param       instance       query  string  false  "Filter by instance"
// @Param       scan_run_id    query  string  false  "Filter by parent scan_run UUID"
// @Param       series_id      query  int     false  "Filter by series ID"
// @Param       season_number  query  int     false  "Filter by season"
// @Param       decision       query  string  false  "Filter by outcome"  Enums(grab,skip,blocked_cooldown,already_optimal,expired)
// @Param       from           query  string  false  "RFC3339 lower bound"
// @Param       to             query  string  false  "RFC3339 upper bound"
// @Success     200  {object}  dto.DecisionList
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /decisions [get]
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
		writeInternalError(c, h.logger, "audit_list_decisions_failed", err,
			slog.String("endpoint", "/api/v1/decisions"),
		)
		return
	}
	out := make([]dto.Decision, 0, len(recs))
	for _, d := range recs {
		out = append(out, toDecisionDTO(d))
	}
	writeListResponse(c, out, next)
}

// ListGrabs handles GET /api/v1/grabs.
//
// @Summary     List grabs
// @Tags        grabs
// @Produce     json
// @Param       limit          query  int     false  "Page size (default 50, max 200)"
// @Param       cursor         query  string  false  "Opaque next_cursor"
// @Param       instance       query  string  false  "Filter by instance"
// @Param       series_id      query  int     false  "Filter by series ID"
// @Param       season_number  query  int     false  "Filter by season"
// @Param       status         query  string  false  "Filter by status"  Enums(grabbed,imported,import_failed,grab_failed,expired)
// @Param       from           query  string  false  "RFC3339 lower bound"
// @Param       to             query  string  false  "RFC3339 upper bound"
// @Success     200  {object}  dto.GrabList
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /grabs [get]
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
		writeInternalError(c, h.logger, "audit_list_grabs_failed", err,
			slog.String("endpoint", "/api/v1/grabs"),
		)
		return
	}
	out := make([]dto.Grab, 0, len(recs))
	for _, r := range recs {
		out = append(out, toGrabDTO(r))
	}
	writeListResponse(c, out, next)
}
