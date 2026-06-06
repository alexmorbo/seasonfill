package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/instance"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

const instanceBodyLimit = 64 << 10

// InstanceCRUDHandler is the GET/POST/PUT/DELETE handler for
// `/api/v1/instances[/:name]` excluding List (kept on
// InstancesHandler.List) and the probe endpoint (in 027b-2).
type InstanceCRUDHandler struct {
	uc     *instance.UseCase
	logger *slog.Logger
}

func NewInstanceCRUDHandler(uc *instance.UseCase, logger *slog.Logger) *InstanceCRUDHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &InstanceCRUDHandler{uc: uc, logger: logger}
}

// Get returns the masked detail of one instance.
//
// @Summary     Get one Sonarr instance (masked)
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.InstanceDetail
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Header      200   {string}  Last-Modified  "RFC1123 of updated_at"
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name} [get]
func (h *InstanceCRUDHandler) Get(c *gin.Context) {
	name := c.Param("name")
	snap, ts, err := h.uc.Get(c.Request.Context(), name)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.Header("Last-Modified", ts.UTC().Format(http.TimeFormat))
	c.JSON(http.StatusOK, snapshotToDetailDTO(snap, ts))
}

// Create persists a new instance row.
//
// @Summary     Create a Sonarr instance
// @Tags        instances
// @Accept      json
// @Produce     json
// @Param       body  body      dto.InstanceCreateRequest  true  "Instance fields"
// @Success     201   {object}  dto.InstanceDetail
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     409   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances [post]
func (h *InstanceCRUDHandler) Create(c *gin.Context) {
	var req dto.InstanceCreateRequest
	if !readJSONBody(c, &req) {
		return
	}
	snap := requestToSnapshot(req)
	if err := h.uc.Create(c.Request.Context(), snap); err != nil {
		h.writeError(c, err)
		return
	}
	// Re-fetch with masked key to send back in the body — matches GET shape.
	stored, ts, err := h.uc.Get(c.Request.Context(), req.Name)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.Header("Last-Modified", ts.UTC().Format(http.TimeFormat))
	c.JSON(http.StatusCreated, snapshotToDetailDTO(stored, ts))
}

// Update mutates an existing instance row (name immutable).
//
// The `If-Unmodified-Since` precondition is enforced at second
// resolution to match the wire `Last-Modified` header (RFC1123,
// second precision). This means a write that lands within the same
// wall-clock second as the client's snapshot is accepted as "not
// stale" — a deliberate 1-second favour-the-client window.
//
// @Summary     Update a Sonarr instance
// @Tags        instances
// @Accept      json
// @Produce     json
// @Param       name  path      string                     true  "Instance name"
// @Param       body  body      dto.InstanceUpdateRequest  true  "Instance fields"
// @Success     200   {object}  dto.InstanceDetail
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     412   {object}  dto.ErrorResponse
// @Header      200   {string}  Last-Modified  "RFC1123 of updated_at"
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name} [put]
func (h *InstanceCRUDHandler) Update(c *gin.Context) {
	name := c.Param("name")
	var req dto.InstanceUpdateRequest
	if !readJSONBody(c, &req) {
		return
	}
	var iusPtr *time.Time
	if raw := c.GetHeader("If-Unmodified-Since"); raw != "" {
		t, err := http.ParseTime(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest,
				dto.ErrorResponse{Error: "If-Unmodified-Since: " + err.Error(), Code: "BAD_REQUEST"})
			return
		}
		iusPtr = &t
	}
	snap := requestToSnapshot(req)
	if err := h.uc.Update(c.Request.Context(), name, snap, iusPtr); err != nil {
		if errors.Is(err, instance.ErrStaleWrite) {
			h.writeStaleWrite(c, name)
			return
		}
		h.writeError(c, err)
		return
	}
	stored, ts, err := h.uc.Get(c.Request.Context(), name)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.Header("Last-Modified", ts.UTC().Format(http.TimeFormat))
	c.JSON(http.StatusOK, snapshotToDetailDTO(stored, ts))
}

// writeStaleWrite emits the 412 STALE_WRITE response with a
// Last-Modified header sourced from the current stored row so the
// caller can re-issue the PUT immediately without an extra GET. If
// the row was deleted between writes the header is omitted and the
// 412 body is still returned (the caller's retry will then 404).
func (h *InstanceCRUDHandler) writeStaleWrite(c *gin.Context, name string) {
	_, ts, err := h.uc.Get(c.Request.Context(), name)
	if err == nil {
		c.Header("Last-Modified", ts.UTC().Format(http.TimeFormat))
	}
	c.AbortWithStatusJSON(http.StatusPreconditionFailed, dto.ErrorResponse{
		Error: "instance was modified by another client", Code: "STALE_WRITE",
	})
}

// Delete hard-deletes an instance row + cascaded history.
//
// @Summary     Delete a Sonarr instance and its history
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     204
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name} [delete]
func (h *InstanceCRUDHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	if err := h.uc.Delete(c.Request.Context(), name); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeError maps usecase errors to wire responses. The mapping is
// deliberate (not via switch on type) because the wire codes are
// stable contract surface. The *instance.ValidationError branch must
// run BEFORE the generic ErrValidation branch so the per-field code
// reaches the wire instead of the generic BAD_REQUEST sentinel.
func (h *InstanceCRUDHandler) writeError(c *gin.Context, err error) {
	var verr *instance.ValidationError
	switch {
	case errors.Is(err, instance.ErrNameImmutable):
		c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: "renaming an instance is not supported; delete and recreate",
			Code:  "NAME_IMMUTABLE",
		})
	case errors.Is(err, instance.ErrDuplicateName):
		c.AbortWithStatusJSON(http.StatusConflict, dto.ErrorResponse{
			Error: "instance name already exists", Code: "DUPLICATE_NAME",
		})
	case errors.As(err, &verr):
		c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: verr.Error(), Code: verr.Code,
		})
	case errors.Is(err, instance.ErrValidation):
		c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: err.Error(), Code: "BAD_REQUEST",
		})
	case errors.Is(err, ports.ErrNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, dto.ErrorResponse{
			Error: "instance not found", Code: "INSTANCE_NOT_FOUND",
		})
	default:
		h.logger.ErrorContext(c.Request.Context(), "instance.crud.error",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, dto.ErrorResponse{
			Error: "internal server error",
		})
	}
}

// readJSONBody enforces application/json + body cap + parses into out.
// Mirrors AuthHandler.readLoginBody so the two paths share an
// envelope shape.
func readJSONBody(c *gin.Context, out any) bool {
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "content-type must be application/json", Code: "BAD_REQUEST"})
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, instanceBodyLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.AbortWithStatusJSON(http.StatusBadRequest,
				dto.ErrorResponse{Error: "payload too large", Code: "BAD_REQUEST"})
			return false
		}
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "cannot read body", Code: "BAD_REQUEST"})
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "malformed body", Code: "BAD_REQUEST"})
		return false
	}
	return true
}

func requestToSnapshot(r dto.InstanceCreateRequest) runtime.InstanceSnapshot {
	return runtime.InstanceSnapshot{
		Name:          r.Name,
		URL:           r.URL,
		APIKey:        r.APIKey,
		Mode:          r.Mode,
		Timeout:       time.Duration(r.TimeoutSec) * time.Second,
		SearchTimeout: time.Duration(r.SearchTimeoutSec) * time.Second,
		DryRun:        r.DryRun,
		Tags: runtime.TagsSnapshot{
			Mode: r.Tags.Mode, Include: r.Tags.Include, Exclude: r.Tags.Exclude,
		},
		Search: runtime.SearchSnapshot{
			RequireAllAired:      r.Search.RequireAllAired,
			SkipSpecials:         r.Search.SkipSpecials,
			SkipAnime:            r.Search.SkipAnime,
			MinCustomFormatScore: r.Search.MinCustomFormatScore,
		},
		Ranking: runtime.RankingSnapshot{
			IndexerPriorityEnabled: r.Ranking.IndexerPriorityEnabled,
			OriginBonus:            r.Ranking.OriginBonus,
		},
		Limits: runtime.LimitsSnapshot{
			ScanMaxSeries: r.Limits.ScanMaxSeries, MaxGrabsPerScan: r.Limits.MaxGrabsPerScan,
		},
		RateLimit: runtime.RateLimitSnapshot{RPM: r.RateLimitRPM, Burst: r.RateLimitBurst},
		Cooldown: runtime.CooldownSnapshot{
			Mode:                  r.Cooldown.Mode,
			SeriesAfterGrab:       time.Duration(r.Cooldown.SeriesAfterGrabSec) * time.Second,
			GUIDAfterFailedGrab:   time.Duration(r.Cooldown.GUIDAfterFailedGrabSec) * time.Second,
			GUIDAfterFailedImport: time.Duration(r.Cooldown.GUIDAfterFailedImportSec) * time.Second,
		},
		Retry: runtime.RetrySnapshot{
			MaxAttempts:    r.Retry.MaxAttempts,
			InitialBackoff: time.Duration(r.Retry.InitialBackoffSec) * time.Second,
			MaxBackoff:     time.Duration(r.Retry.MaxBackoffSec) * time.Second,
		},
		HealthCheck: runtime.HealthCheckSnapshot{
			RecheckAuth:    time.Duration(r.HealthCheck.RecheckAuthSec) * time.Second,
			RecheckNetwork: time.Duration(r.HealthCheck.RecheckNetworkSec) * time.Second,
		},
		PublicURL:             r.PublicURL,
		WebhookInstallEnabled: webhookInstallEnabledOrDefault(r.WebhookInstallEnabled),
		WebhookURLOverride:    r.WebhookURLOverride,
	}
}

// webhookInstallEnabledOrDefault collapses the request pointer to a
// concrete snapshot bool. Nil (JSON key omitted) defaults to true to
// match the 041 migration default and the "every existing row already
// has the webhook installed" invariant. A non-nil pointer wins
// verbatim — including explicit false.
func webhookInstallEnabledOrDefault(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

func snapshotToDetailDTO(s runtime.InstanceSnapshot, ts time.Time) dto.InstanceDetail {
	return dto.InstanceDetail{
		Name: s.Name, URL: s.URL, APIKey: "***", Mode: s.Mode,
		TimeoutSec:       int(s.Timeout / time.Second),
		SearchTimeoutSec: int(s.SearchTimeout / time.Second),
		DryRun:           s.DryRun,
		Tags: dto.InstanceTags{
			Mode: s.Tags.Mode, Include: s.Tags.Include, Exclude: s.Tags.Exclude,
		},
		Search: dto.InstanceSearch{
			RequireAllAired:      s.Search.RequireAllAired,
			SkipSpecials:         s.Search.SkipSpecials,
			SkipAnime:            s.Search.SkipAnime,
			MinCustomFormatScore: s.Search.MinCustomFormatScore,
		},
		Ranking: dto.InstanceRanking{
			IndexerPriorityEnabled: s.Ranking.IndexerPriorityEnabled,
			OriginBonus:            s.Ranking.OriginBonus,
		},
		Limits: dto.InstanceLimits{
			ScanMaxSeries: s.Limits.ScanMaxSeries, MaxGrabsPerScan: s.Limits.MaxGrabsPerScan,
		},
		RateLimitRPM:   s.RateLimit.RPM,
		RateLimitBurst: s.RateLimit.Burst,
		Cooldown: dto.InstanceCooldown{
			Mode:                     s.Cooldown.Mode,
			SeriesAfterGrabSec:       int(s.Cooldown.SeriesAfterGrab / time.Second),
			GUIDAfterFailedGrabSec:   int(s.Cooldown.GUIDAfterFailedGrab / time.Second),
			GUIDAfterFailedImportSec: int(s.Cooldown.GUIDAfterFailedImport / time.Second),
		},
		Retry: dto.InstanceRetry{
			MaxAttempts:       s.Retry.MaxAttempts,
			InitialBackoffSec: int(s.Retry.InitialBackoff / time.Second),
			MaxBackoffSec:     int(s.Retry.MaxBackoff / time.Second),
		},
		HealthCheck: dto.InstanceHealthCheck{
			RecheckAuthSec:    int(s.HealthCheck.RecheckAuth / time.Second),
			RecheckNetworkSec: int(s.HealthCheck.RecheckNetwork / time.Second),
		},
		PublicURL:             s.PublicURL,
		WebhookInstallEnabled: s.WebhookInstallEnabled,
		WebhookURLOverride:    s.WebhookURLOverride,
		UIURL:                 s.UIURL(),
		UpdatedAt:             ts.UTC(),
	}
}
