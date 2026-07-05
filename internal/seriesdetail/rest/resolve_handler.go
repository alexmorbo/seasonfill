// Package rest — seriesdetail HTTP handlers.
//
// resolve_handler.go (BE-3, card-unification). GET /api/v1/series/resolve
// ?tmdb_id=<int> — lazy resolve-or-create of a canonical series.id from a
// TMDB id so the unified series card can always route internally to
// /series/:id. Delegates to ResolveUseCase; existing canon rows return
// their id, unknown tmdb_ids get a stub + enqueued enrichment.
package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// ResolveHandler powers GET /api/v1/series/resolve.
type ResolveHandler struct {
	uc     *seriesdetail.ResolveUseCase
	logger *slog.Logger
}

// NewResolveHandler constructs the handler. uc is required; logger=nil
// falls back to slog.Default.
func NewResolveHandler(uc *seriesdetail.ResolveUseCase, logger *slog.Logger) *ResolveHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResolveHandler{uc: uc, logger: logger}
}

// Resolve handles GET /api/v1/series/resolve.
//
// @Summary     Resolve a TMDB id to a canonical series id (global)
// @Description Returns the canonical series.id for a TMDB TV id so the
// @Description unified series card can route internally to /series/:id.
// @Description Existing canon rows return their id unchanged; an unknown
// @Description tmdb_id gets a minimal canon stub (hydration=stub) created
// @Description and its enrichment enqueued at PriorityHot so a subsequent
// @Description detail render lands on hydrated data. This app is
// @Description series-only — the caller sends only TV tmdb ids.
// @Tags        series
// @Produce     json
// @Param       tmdb_id  query     int  true  "TMDB TV id (positive integer)"
// @Success     200      {object}  dto.SeriesResolveResponse
// @Failure     400      {object}  dto.ErrorResponse
// @Failure     401      {object}  dto.ErrorResponse
// @Failure     500      {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/resolve [get]
func (h *ResolveHandler) Resolve(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("tmdb_id"))
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "tmdb_id must be a positive integer"})
		return
	}

	seriesID, err := h.uc.ResolveByTMDB(c.Request.Context(), domain.TMDBID(parsed))
	if err != nil {
		if errors.Is(err, seriesdetail.ErrInvalidTMDBID) {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "tmdb_id must be a positive integer"})
			return
		}
		_ = c.Error(err) // middleware maps to 500
		return
	}
	c.JSON(http.StatusOK, dto.SeriesResolveResponse{SeriesID: seriesID})
}
