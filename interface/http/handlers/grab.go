package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appgrab "github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	domaindecision "github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// GrabHandler exposes POST /decisions/{id}/grab — explicit-confirm
// path that bypasses global dry_run for one decision. Idempotent on
// (instance, series, season, release_guid).
type GrabHandler struct {
	decisions ports.DecisionRepository
	grabs     ports.GrabRepository
	cooldowns ports.CooldownRepository
	grabUC    *appgrab.UseCase
	instances map[string]scan.Instance
	logger    *slog.Logger
}

// NewGrabHandler — `cooldowns`/`instances` nil-OK for route-shape-only
// tests (e.g. docs_test).
func NewGrabHandler(decisions ports.DecisionRepository, grabs ports.GrabRepository,
	cooldowns ports.CooldownRepository, grabUC *appgrab.UseCase,
	instances map[string]scan.Instance, logger *slog.Logger) *GrabHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GrabHandler{decisions: decisions, grabs: grabs, cooldowns: cooldowns,
		grabUC: grabUC, instances: instances, logger: logger}
}

// ByDecision handles POST /api/v1/decisions/{id}/grab.
//
// @Summary     Grab the release the decision selected
// @Description Explicit-confirm path that bypasses global dry_run for
// @Description one decision. Idempotent on (instance, series, season,
// @Description release_guid). Eligible only when decision == "grab",
// @Description selected_guid != "" and dry_run_would_grab == true.
// @Tags        decisions
// @Produce     json
// @Param       id   path      string  true  "Decision UUID"
// @Success     200  {object}  dto.Grab
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     404  {object}  dto.ErrorResponse
// @Failure     409  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Failure     502  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /decisions/{id}/grab [post]
func (h *GrabHandler) ByDecision(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid id")
		return
	}

	d, err := h.decisions.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeError(c, http.StatusNotFound, "decision not found")
			return
		}
		writeInternalError(c, h.logger, "grab_get_decision_failed", err,
			slog.String("endpoint", "/api/v1/decisions/:id/grab"),
			slog.String("decision_id", id.String()))
		return
	}

	if d.Outcome != domaindecision.OutcomeGrab ||
		d.Selected == nil || d.Selected.Release.GUID == "" {
		writeError(c, http.StatusBadRequest, "decision did not select a release")
		return
	}
	if !d.DryRunWouldGrab {
		writeError(c, http.StatusConflict, "already executed at scan time")
		return
	}

	inst, ok := h.instances[d.InstanceName]
	if !ok {
		h.logger.WarnContext(ctx, "grab_unknown_instance",
			slog.String("decision_id", id.String()),
			slog.String("instance", d.InstanceName))
		writeError(c, http.StatusNotFound, "unknown instance: "+d.InstanceName)
		return
	}

	if existingID, found, lookupErr := h.findExistingGrab(ctx, d); lookupErr != nil {
		writeInternalError(c, h.logger, "grab_idempotency_lookup_failed", lookupErr,
			slog.String("decision_id", id.String()))
		return
	} else if found {
		c.JSON(http.StatusConflict, dto.ErrorResponse{
			Error: "already grabbed: " + existingID.String()})
		return
	}

	if blockedKey, blockedErr := h.checkCooldowns(ctx, d); blockedErr != nil {
		writeInternalError(c, h.logger, "grab_cooldown_lookup_failed", blockedErr,
			slog.String("decision_id", id.String()))
		return
	} else if blockedKey != "" {
		c.JSON(http.StatusConflict, dto.ErrorResponse{
			Error: "blocked by cooldown: " + blockedKey})
		return
	}

	out := h.grabUC.Execute(ctx, appgrab.Input{
		ScanRunID:    d.ScanRunID,
		InstanceName: d.InstanceName,
		SeriesID:     d.SeriesID,
		SeriesTitle:  d.SeriesTitle,
		SeasonNumber: d.SeasonNumber,
		Selected:     *d.Selected,
		Coverage:     d.Selected.Coverage,
		Sonarr:       inst.Client,
		Config: appgrab.Config{
			MaxAttempts:    inst.Config.Retry.MaxAttempts,
			InitialBackoff: inst.Config.Retry.InitialBackoff,
			MaxBackoff:     inst.Config.Retry.MaxBackoff,
			SeriesCooldown: inst.Config.Cooldown.SeriesAfterGrab,
			GUIDCooldown:   inst.Config.Cooldown.GUIDAfterFailedGrab,
		},
	})
	if out.Err != nil {
		// Race-recovery: the unique (instance,series,season,release_guid)
		// index admits exactly one INSERT when two concurrent POSTs both
		// pass the fast-path check. The loser's Create fails with
		// ErrGrabDuplicate; we resolve the survivor and return it 200 so
		// the caller sees idempotent success rather than spurious 500.
		// Sonarr ForceGrab on the loser may have already fired but
		// Sonarr dedupes on releaseId server-side.
		if errors.Is(out.Err, repositories.ErrGrabDuplicate) {
			rec, ferr := h.grabs.FindExisting4Tuple(ctx, d.InstanceName,
				d.SeriesID, d.SeasonNumber, d.Selected.Release.GUID)
			if ferr != nil {
				writeInternalError(c, h.logger, "grab_duplicate_resolve_failed", ferr,
					slog.String("decision_id", id.String()))
				return
			}
			h.logger.InfoContext(ctx, "grab_by_decision_race_idempotent",
				slog.String("decision_id", id.String()),
				slog.String("grab_id", rec.ID.String()))
			c.JSON(http.StatusOK, toGrabDTO(rec))
			return
		}
		status, msg, lvl := http.StatusInternalServerError, "grab failed", "grab_execute_failed"
		switch {
		case errors.Is(out.Err, domain.ErrInstanceUnauthorized):
			status, msg, lvl = http.StatusBadGateway, "sonarr unauthorized", "grab_upstream_unauthorized"
		case errors.Is(out.Err, domain.ErrInstanceNetwork):
			status, msg, lvl = http.StatusBadGateway, "sonarr unavailable", "grab_upstream_network_error"
		}
		h.logger.LogAttrs(ctx, slog.LevelWarn, lvl,
			slog.String("decision_id", id.String()),
			slog.String("instance", d.InstanceName),
			slog.String("error", out.Err.Error()))
		c.JSON(status, dto.ErrorResponse{Error: msg})
		return
	}

	h.logger.InfoContext(ctx, "grab_by_decision_succeeded",
		slog.String("decision_id", id.String()),
		slog.String("instance", d.InstanceName),
		slog.Int("series_id", d.SeriesID),
		slog.Int("season", d.SeasonNumber),
		slog.String("guid", d.Selected.Release.GUID),
		slog.String("grab_id", out.Record.ID.String()))
	c.JSON(http.StatusOK, toGrabDTO(out.Record))
}

// findExistingGrab walks the (instance,series,season) partition and
// returns the first row matching the GUID. 200-row cap covers any
// realistic season; M-011a-1 swaps in an indexed query.
func (h *GrabHandler) findExistingGrab(ctx context.Context, d domaindecision.Decision) (uuid.UUID, bool, error) {
	inst := d.InstanceName
	sid := d.SeriesID
	season := d.SeasonNumber
	recs, _, err := h.grabs.List(ctx,
		ports.GrabFilter{Instance: &inst, SeriesID: &sid, SeasonNumber: &season},
		ports.Pagination{Limit: 200})
	if err != nil {
		return uuid.Nil, false, err
	}
	guid := d.Selected.Release.GUID
	for _, r := range recs {
		if r.ReleaseGUID == guid {
			return r.ID, true, nil
		}
	}
	return uuid.Nil, false, nil
}

// checkCooldowns returns the active cooldown key (series or guid) if
// either applies; "" + nil = clear to proceed.
func (h *GrabHandler) checkCooldowns(ctx context.Context, d domaindecision.Decision) (string, error) {
	if h.cooldowns == nil {
		return "", nil
	}
	now := time.Now().UTC()
	sKey := cooldown.SeriesKey(d.InstanceName, d.SeriesID, d.SeasonNumber)
	active, err := h.cooldowns.FilterActive(ctx, cooldown.ScopeSeries, []string{sKey}, now)
	if err != nil {
		return "", err
	}
	if len(active) > 0 {
		return "series:" + sKey, nil
	}
	gKey := cooldown.GUIDKey(d.Selected.Release.GUID)
	active, err = h.cooldowns.FilterActive(ctx, cooldown.ScopeGUID, []string{gKey}, now)
	if err != nil {
		return "", err
	}
	if len(active) > 0 {
		return "guid:" + gKey, nil
	}
	return "", nil
}
