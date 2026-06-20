package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appdecision "github.com/alexmorbo/seasonfill/application/decision"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	adminrest "github.com/alexmorbo/seasonfill/internal/admin/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// AuditHandler exposes the four read-only audit endpoints. Constructor
// takes port interfaces so tests can substitute fakes (though in-package
// tests use real GORM repos against in-memory SQLite for stronger
// coverage).
type AuditHandler struct {
	scans          ports.ScanRepository
	decisions      ports.DecisionRepository
	grabs          ports.GrabRepository
	seriesCache    ports.SeriesCacheRepository
	mediaPending   adminrest.CatalogMediaPendingWriter // story 352, nil-OK
	mediaPrewarmer adminrest.CatalogMediaPrewarmer     // story 352, nil-OK
	logger         *slog.Logger
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

// WithSeriesCache wires the read-side of series_cache so ListGrabs
// can enrich each row with the authoritative Sonarr slug from
// (instance_name, series_id). Mirrors the 041g pattern used by
// InstancesHandler. nil repo (the default) leaves grab.title_slug
// unset on the wire — the SPA falls back to its client-side
// slugifier, the pre-116 behaviour. Builder pattern keeps the
// constructor signature stable across the existing
// audit_test.go call sites.
func (h *AuditHandler) WithSeriesCache(repo ports.SeriesCacheRepository) *AuditHandler {
	h.seriesCache = repo
	return h
}

// WithMediaPending wires the catalog-side EnsurePending kick so
// /grabs (which projects an eager poster_hash via
// collectGrabCacheFields) also lands a pending media_assets row
// keyed on the same hash. nil writer = no-op (test fixtures /
// minimal boot).
//
// Story 352.
func (h *AuditHandler) WithMediaPending(w adminrest.CatalogMediaPendingWriter) *AuditHandler {
	h.mediaPending = w
	return h
}

// WithMediaPrewarmer wires the optional downloader-enqueue kick.
// nil-OK — see story 352 MVP scope.
func (h *AuditHandler) WithMediaPrewarmer(p adminrest.CatalogMediaPrewarmer) *AuditHandler {
	h.mediaPrewarmer = p
	return h
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

// instanceNamePtrFromQuery is the typed counterpart of
// stringPtrFromQuery for the now-typed Instance filter fields
// (*domain.InstanceName). Returns nil for empty / absent values.
func instanceNamePtrFromQuery(c *gin.Context, name string) *domain.InstanceName {
	v := c.Query(name)
	if v == "" {
		return nil
	}
	n := domain.InstanceName(v)
	return &n
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
		ID:               d.ID.String(),
		ScanRunID:        d.ScanRunID.String(),
		Instance:         d.InstanceName,
		SeriesID:         d.SeriesID,
		SeriesTitle:      d.SeriesTitle,
		SeasonNumber:     d.SeasonNumber,
		Decision:         string(d.Outcome),
		Reason:           string(d.Reason),
		Category:         string(appdecision.Classify(string(d.Reason))),
		MissingCount:     d.MissingCount,
		ExistingCount:    d.ExistingCount,
		ReleasesFound:    d.ReleasesFound,
		CandidatesCount:  d.CandidatesCount,
		SelectedGUID:     selectedGUID,
		DryRunWouldGrab:  d.DryRunWouldGrab,
		ErrorDetail:      d.ErrorDetail,
		SupersededByID:   supersededByIDString(d.SupersededByID),
		TotalEpisodes:    d.TotalEpisodes,
		AiredEpisodes:    d.AiredEpisodes,
		ExistingEpisodes: d.ExistingEpisodes,
		GrabbedEpisodes:  d.GrabbedEpisodes,
		Intent:           intentToDTO(d.Intent),
		CreatedAt:        d.CreatedAt,
	}
}

// intentToDTO lifts a *decision.Intent onto a *dto.DecisionIntent.
// nil in → nil out (DTO emits `null`). Non-nil zero-valued Intent
// → non-nil DTO with empty arrays + empty strings so the SPA can
// always indexing without branching on inner nil. 091a / F-P2-2.
func intentToDTO(i *decision.Intent) *dto.DecisionIntent {
	if i == nil {
		return nil
	}
	target := i.TargetEpisodes
	if target == nil {
		target = []int{}
	}
	had := i.HadEpisodes
	if had == nil {
		had = []int{}
	}
	return &dto.DecisionIntent{
		TargetEpisodes:     target,
		HadEpisodes:        had,
		ChosenBecause:      string(i.ChosenBecause),
		ChosenReasonDetail: i.ChosenReasonDetail,
	}
}

func supersededByIDString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func toGrabDTO(r grab.Record, replayedBy []uuid.UUID, parent *grab.Record, intent *decision.Intent, titleSlug string, posterHash *string) dto.Grab {
	d := dto.Grab{
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
		TorrentHash:       r.TorrentHash,
		SizeBytes:         r.SizeBytes,
		Parsed:            grabParsedToDTO(r.Parsed),
		ParsedAt:          r.ParsedAt,
		Intent:            intentToDTO(intent),
		TitleSlug:         titleSlug,
		PosterHash:        posterHash,
	}
	if r.ReplayOfID != nil {
		s := r.ReplayOfID.String()
		d.ReplayOfID = &s
	}
	if len(replayedBy) > 0 {
		d.ReplayedBy = make([]string, 0, len(replayedBy))
		for _, c := range replayedBy {
			d.ReplayedBy = append(d.ReplayedBy, c.String())
		}
	}
	if kind := grab.DeriveReplayKind(r, parent); kind != grab.ReplayKindPrimary {
		d.ReplayKind = string(kind)
	}
	return d
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
		Instance: instanceNamePtrFromQuery(c, "instance"),
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
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toScanDTO(rec))
}

// GetDecision handles GET /api/v1/decisions/:id.
//
// Used by DecisionDrawer to deep-load a decision row when the
// `?drawer=<id>` URL is opened past the first paginated /decisions
// page (N-4). Pure read; no side effects. Mirrors GetScan.
//
// @Summary     Get decision by ID
// @Tags        decisions
// @Produce     json
// @Param       id    path     string  true  "Decision UUID"
// @Success     200   {object}  dto.Decision
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /decisions/{id} [get]
func (h *AuditHandler) GetDecision(c *gin.Context) {
	raw := c.Param("id")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid id")
		return
	}
	rec, err := h.decisions.GetByID(c.Request.Context(), id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toDecisionDTO(rec))
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
// @Param       decision       query  string  false  "Filter by outcome"  Enums(grab,skip,blocked_cooldown,already_optimal,expired,error)
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
	var sonarrSeriesID *domain.SonarrSeriesID
	if seriesID != nil {
		v := domain.SonarrSeriesID(*seriesID)
		sonarrSeriesID = &v
	}

	filter := ports.DecisionFilter{
		From:         from,
		To:           to,
		SeriesID:     sonarrSeriesID,
		SeasonNumber: season,
		Instance:     instanceNamePtrFromQuery(c, "instance"),
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
	var sonarrSeriesID *domain.SonarrSeriesID
	if seriesID != nil {
		v := domain.SonarrSeriesID(*seriesID)
		sonarrSeriesID = &v
	}

	filter := ports.GrabFilter{
		From:         from,
		To:           to,
		SeriesID:     sonarrSeriesID,
		SeasonNumber: season,
		Instance:     instanceNamePtrFromQuery(c, "instance"),
		Status:       stringPtrFromQuery(c, "status"),
	}
	ctx := c.Request.Context()
	recs, next, err := h.grabs.List(ctx, filter, ports.Pagination{Limit: limit, Cursor: cursor})
	if err != nil {
		writeInternalError(c, h.logger, "audit_list_grabs_failed", err,
			slog.String("endpoint", "/api/v1/grabs"))
		return
	}

	// 043a: batched reverse-lookup for replay_of_id pointers. One SQL
	// hit per page; failure here downgrades to no-replays metadata
	// (page still renders).
	parentIDs := make([]uuid.UUID, 0, len(recs))
	for _, r := range recs {
		parentIDs = append(parentIDs, r.ID)
	}
	replays, repErr := h.grabs.ListReplaysOf(ctx, parentIDs)
	if repErr != nil {
		h.logger.WarnContext(ctx, "audit_list_grabs_replays_fanout_failed",
			slog.String("endpoint", "/api/v1/grabs"),
			slog.String("error", repErr.Error()))
		replays = map[uuid.UUID][]uuid.UUID{}
	}

	// F-P2-3: resolve parent records so toGrabDTO can derive replay_kind.
	// Build an in-page lookup first — most parents are on the same page
	// (newest-first paging keeps replays near their parents). Cross-page
	// parents fall back to GetByID; production page sizes (<=200) make
	// N tiny. A miss here downgrades to ReplayKindOther; the row still
	// renders.
	parents := make(map[uuid.UUID]grab.Record, len(recs))
	inPage := make(map[uuid.UUID]grab.Record, len(recs))
	for _, r := range recs {
		inPage[r.ID] = r
	}
	for _, r := range recs {
		if r.ReplayOfID == nil {
			continue
		}
		pid := *r.ReplayOfID
		if _, ok := parents[pid]; ok {
			continue
		}
		if p, ok := inPage[pid]; ok {
			parents[pid] = p
			continue
		}
		p, perr := h.grabs.GetByID(ctx, pid)
		if perr != nil {
			h.logger.WarnContext(ctx, "audit_list_grabs_replay_parent_lookup_failed",
				slog.String("endpoint", "/api/v1/grabs"),
				slog.String("parent_id", pid.String()),
				slog.String("error", perr.Error()))
			continue
		}
		parents[pid] = p
	}

	// 091a / F-P2-2: per-page Decision lookup so GrabDrawer can render
	// the "why this grab" intent without a second round-trip. We key
	// on the grab's scan_run_id and pick the latest Decision row for
	// (scan_run_id, instance, series_id, season_number). Best-effort
	// — a miss (no Decision OR Decision has nil Intent) leaves the
	// Grab DTO's intent field nil, which the frontend handles.
	intents := h.collectGrabIntents(ctx, recs)

	// 116 + 348b: per-page series_cache lookup so the SPA can render
	// the authoritative Sonarr deep-link (slug) and the
	// content-addressed poster URL (hash) without a fanout. One repo
	// call per distinct instance on the page — production usage is
	// almost always single-instance (the /grabs page passes
	// ?instance=… in every real call), and the upper bound is the
	// number of configured Sonarr instances (typically 1-3). Nil
	// repo (constructor default in tests) or repo error degrades
	// to no slugs / no hashes; the page still renders.
	slugs, hashes := h.collectGrabCacheFields(ctx, recs)

	out := make([]dto.Grab, 0, len(recs))
	for _, r := range recs {
		var parent *grab.Record
		if r.ReplayOfID != nil {
			if p, ok := parents[*r.ReplayOfID]; ok {
				parent = &p
			}
		}
		key := grabKey{instance: r.InstanceName, seriesID: r.SeriesID}
		out = append(out, toGrabDTO(r, replays[r.ID], parent, intents[r.ID], slugs[key], hashes[key]))
	}
	writeListResponse(c, out, next)
}

// collectGrabIntents resolves the per-grab Intent payload for the
// supplied page of grab records. One Decision lookup per grab keyed
// on (scan_run_id, instance, series_id, season_number): we use the
// existing DecisionRepository.List filter API rather than introduce a
// new bulk endpoint, because the page size is bounded (<=200) and the
// indexes on (scan_run_id, instance_name, series_id, season_number)
// already make each lookup an index seek. Misses are logged at WARN
// and the offending grab degrades to no-intent (UI placeholder).
//
// 091a / F-P2-2.
func (h *AuditHandler) collectGrabIntents(ctx context.Context, recs []grab.Record) map[uuid.UUID]*decision.Intent {
	out := make(map[uuid.UUID]*decision.Intent, len(recs))
	if h.decisions == nil {
		return out
	}
	for _, r := range recs {
		// Empty scan_run_id is impossible for a real row (always
		// uuid.New on Create), but defend against test fixtures.
		if r.ScanRunID == uuid.Nil {
			continue
		}
		scanID := r.ScanRunID
		inst := r.InstanceName
		sid := r.SeriesID
		season := r.SeasonNumber
		decs, _, err := h.decisions.List(ctx, ports.DecisionFilter{
			ScanRunID:    &scanID,
			Instance:     &inst,
			SeriesID:     &sid,
			SeasonNumber: &season,
		}, ports.Pagination{Limit: 1})
		if err != nil {
			h.logger.WarnContext(ctx, "audit_list_grabs_intent_lookup_failed",
				slog.String("grab_id", r.ID.String()),
				slog.String("scan_run_id", scanID.String()),
				slog.String("error", err.Error()))
			continue
		}
		if len(decs) == 0 {
			continue
		}
		out[r.ID] = decs[0].Intent
	}
	return out
}

// grabKey scopes a series_cache lookup to (instance, series_id).
// Used by collectGrabCacheFields to deduplicate per-page lookups
// across multiple grabs of the same series.
type grabKey struct {
	instance domain.InstanceName
	seriesID domain.SonarrSeriesID
}

// collectGrabCacheFields builds two (instance, series_id) → value
// maps from one ListActiveByInstance call per distinct instance: the
// authoritative-title-slug map (116) and the poster-hash map (348b).
// Production usage is almost always single-instance (the /grabs page
// passes ?instance=… in every real call), so this is a single repo
// hit per request — the hashes ride along for free. Errors WARN-log
// and degrade to empty maps for that instance; callers see an empty
// slug (SPA falls back to its client-side slugifier) and a nil
// poster_hash (DTO omits the field, FE falls back to monogram).
//
// Returns nil-safe even when h.seriesCache is unwired (the audit_
// test.go fixture path used to do this before 348b).
//
// 116 + 348b. Mirrors the 041g pattern on enrichMissingFromCache.
func (h *AuditHandler) collectGrabCacheFields(ctx context.Context, recs []grab.Record) (map[grabKey]string, map[grabKey]*string) {
	slugs := make(map[grabKey]string, len(recs))
	hashes := make(map[grabKey]*string, len(recs))
	if h.seriesCache == nil || len(recs) == 0 {
		return slugs, hashes
	}
	// Distinct instances on this page.
	instances := make(map[domain.InstanceName]struct{}, 1)
	for _, r := range recs {
		if r.InstanceName == "" {
			continue
		}
		instances[r.InstanceName] = struct{}{}
	}
	// Story 352: gather every entry seen across all instances so the
	// EnsurePending kick covers exactly the hashes the wire DTO will
	// carry. Allocated outside the for loop so the kick batches one
	// goroutine per request rather than one per instance.
	var pendingEntries []adminrest.CatalogPosterEntry
	for inst := range instances {
		entries, err := h.seriesCache.ListActiveByInstance(ctx, inst)
		if err != nil {
			h.logger.WarnContext(ctx, "audit_list_grabs_cache_lookup_failed",
				slog.String("endpoint", "/api/v1/grabs"),
				slog.String("instance", string(inst)),
				slog.String("error", err.Error()))
			continue
		}
		for _, e := range entries {
			key := grabKey{instance: inst, seriesID: e.SonarrSeriesID}
			if e.TitleSlug != "" {
				slugs[key] = e.TitleSlug
			}
			// Derive the content-addressed media hash from the raw
			// canon poster path — no dependency on media_assets row
			// existence. The media handler's on-demand fetch fills the
			// bytes when the FE first requests them.
			if hash := mediaHashForPosterAsset(e.PosterAsset); hash != nil {
				hashes[key] = hash
				pendingEntries = append(pendingEntries, adminrest.CatalogPosterEntry{PosterAsset: e.PosterAsset})
			}
		}
	}
	// Story 352: best-effort fire-and-forget. nil mediaPending = no-op.
	if h.mediaPending != nil && len(pendingEntries) > 0 {
		adminrest.KickEnsurePendingForCatalog(ctx, h.mediaPending, h.mediaPrewarmer,
			pendingEntries, adminrest.CatalogPosterKindW342, h.logger)
	}
	return slugs, hashes
}

// grabParsedToDTO lifts a *grab.Parsed onto a *dto.GrabParsed. nil in →
// nil out — the API emits `parsed: null` (or omits the key entirely
// thanks to omitempty). Non-nil zero-valued Parsed → non-nil
// GrabParsed{}, all fields omitted by json tags. Matches the absent vs
// empty distinction documented on domain/grab.Record.Parsed.
func grabParsedToDTO(p *grab.Parsed) *dto.GrabParsed {
	if p == nil {
		return nil
	}
	out := &dto.GrabParsed{
		Codec: p.Codec, Source: p.Source, Quality: p.Quality,
		Resolution: p.Resolution, Dub: p.Dub,
		ReleaseGroup: p.ReleaseGroup,
	}
	if len(p.HDRFlags) > 0 {
		out.HDRFlags = append([]string(nil), p.HDRFlags...)
	}
	if len(p.Languages) > 0 {
		out.Languages = append([]string(nil), p.Languages...)
	}
	if len(p.Subs) > 0 {
		out.Subs = append([]string(nil), p.Subs...)
	}
	return out
}
