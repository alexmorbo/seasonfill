package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	domaindecision "github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	appgrab "github.com/alexmorbo/seasonfill/internal/grab/app"
)

// GrabHandler exposes POST /decisions/{id}/grab — explicit-confirm
// path that bypasses global dry_run for one decision. Idempotent on
// (instance, series, season, release_guid).
type GrabHandler struct {
	decisions ports.DecisionRepository
	grabs     ports.GrabRepository
	cooldowns ports.CooldownRepository
	grabUC    *appgrab.UseCase
	reg       handlers.InstanceRegistry
	logger    *slog.Logger
}

// NewGrabHandler — `cooldowns` nil-OK for route-shape-only tests
// (e.g. docs_test). reg.Load nil-OK for the same reason.
func NewGrabHandler(decisions ports.DecisionRepository, grabs ports.GrabRepository,
	cooldowns ports.CooldownRepository, grabUC *appgrab.UseCase,
	reg handlers.InstanceRegistry, logger *slog.Logger) *GrabHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GrabHandler{decisions: decisions, grabs: grabs, cooldowns: cooldowns,
		grabUC: grabUC, reg: reg, logger: logger}
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
		handlers.WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}

	d, err := h.decisions.GetByID(ctx, id)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if d.Outcome != domaindecision.OutcomeGrab ||
		d.Selected == nil || d.Selected.Release.GUID == "" {
		handlers.WriteError(c, http.StatusBadRequest, "decision did not select a release")
		return
	}
	if !d.DryRunWouldGrab {
		handlers.WriteError(c, http.StatusConflict, "already executed at scan time")
		return
	}

	inst, ok := h.reg.Snapshot()[string(d.InstanceName)]
	if !ok {
		h.logger.WarnContext(ctx, "grab_unknown_instance",
			slog.String("decision_id", id.String()),
			slog.String("instance", string(d.InstanceName)))
		handlers.WriteError(c, http.StatusNotFound, "unknown instance: "+string(d.InstanceName))
		return
	}

	if existingID, found, lookupErr := h.findExistingGrab(ctx, d); lookupErr != nil {
		handlers.WriteInternalError(c, h.logger, "grab_idempotency_lookup_failed", lookupErr,
			slog.String("decision_id", id.String()))
		return
	} else if found {
		c.JSON(http.StatusConflict, dto.ErrorResponse{
			Error: "already grabbed: " + existingID.String()})
		return
	}

	if blockedKey, blockedErr := h.checkCooldowns(ctx, d); blockedErr != nil {
		handlers.WriteInternalError(c, h.logger, "grab_cooldown_lookup_failed", blockedErr,
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
		status, msg, lvl := http.StatusInternalServerError, "grab failed", "grab_execute_failed"
		switch {
		case errors.Is(out.Err, domain.ErrInstanceUnauthorized):
			status, msg, lvl = http.StatusBadGateway, "sonarr unauthorized", "grab_upstream_unauthorized"
		case errors.Is(out.Err, domain.ErrInstanceNetwork):
			status, msg, lvl = http.StatusBadGateway, "sonarr unavailable", "grab_upstream_network_error"
		}
		h.logger.LogAttrs(ctx, slog.LevelWarn, lvl,
			slog.String("decision_id", id.String()),
			slog.String("instance", string(d.InstanceName)),
			slog.String("error", out.Err.Error()))
		c.JSON(status, dto.ErrorResponse{Error: msg})
		return
	}

	h.logger.InfoContext(ctx, "grab_by_decision_succeeded",
		slog.String("decision_id", id.String()),
		slog.String("instance", string(d.InstanceName)),
		slog.Int("series_id", int(d.SeriesID)),
		slog.Int("season", d.SeasonNumber),
		slog.String("guid", d.Selected.Release.GUID),
		slog.String("grab_id", out.Record.ID.String()))
	// 043a: manual-grab path is never a re-grab target on creation —
	// the row was just inserted with replay_of_id=NULL. Pass nil to
	// skip the now-required ListReplaysOf fan-out. Parent is nil too;
	// DeriveReplayKind short-circuits to ReplayKindPrimary which the
	// DTO omits from wire. 091a / F-P2-2: intent is also nil here —
	// the decision row exists (we just GetByID'd it) but loading and
	// shipping it on this single-row 200 path adds latency for a
	// drawer field the SPA can re-fetch via GET /decisions/:id. The
	// frontend treats nil intent here as "fetch on drawer open".
	c.JSON(http.StatusOK, handlers.ToGrabDTO(out.Record, nil, nil, nil, "", nil))
}

// findExistingGrab returns the first non-terminal row for this
// (instance, series, season, GUID). Terminal rows (grab_failed,
// import_failed, imported) do not block — multiple grab attempts on
// the same release are allowed, the user can retry without manual
// cleanup. 200-row cap covers any realistic season.
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
		if r.ReleaseGUID != guid {
			continue
		}
		switch string(r.Status) {
		case "grab_failed", "import_failed", "imported":
			continue
		}
		return r.ID, true, nil
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
