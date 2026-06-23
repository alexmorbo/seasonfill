// Package rest — catalog HTTP handlers.
//
// global_catalog_handler.go (Story 491 / N-1a). GET /api/v1/series and
// GET /api/v1/series/networks — global-namespace wrappers over the per-
// instance ListSeriesCache + ListSeriesCacheNetworks paths. The repo
// layer is unchanged; the new handlers re-use the existing
// InstancesHandler methods after parsing `?instance=` from query.
//
// Multi-instance aggregation is OUT OF SCOPE — `?instance=` is REQUIRED.
// N-2 (Discovery) may add an aggregating list endpoint later; story 491
// matches the legacy semantic (one-instance scope) while exposing the
// new URL shape.
package rest

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// GlobalCatalogHandler exposes /api/v1/series (list) and
// /api/v1/series/networks (facet) under the new global namespace. It
// delegates to the InstancesHandler by setting `c.Params` to include
// `name` before forwarding — keeps the existing query-parsing /
// pagination / facet logic 1:1.
type GlobalCatalogHandler struct {
	inner  *InstancesHandler
	logger *slog.Logger
}

// NewGlobalCatalogHandler constructs the handler. inner = the per-instance
// handler already wired in server.NewServer. logger nil-OK.
func NewGlobalCatalogHandler(inner *InstancesHandler, logger *slog.Logger) *GlobalCatalogHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalCatalogHandler{inner: inner, logger: logger}
}

// List handles GET /api/v1/series.
//
// @Summary     List series-cache entries (global)
// @Description Global series-cache list. Required query param: instance —
// @Description the global namespace replaces the per-instance route that
// @Description was deleted at N-1b (story 492); multi-instance aggregation
// @Description is out of scope (see N-2 Discovery). Supports filter
// @Description (state, q, monitored, networks), sort (updated_desc,
// @Description title_asc, air_date_desc), and keyset pagination.
// @Tags        series
// @Produce     json
// @Param       instance       query  string  true   "Instance name"
// @Param       state          query  string  false  "all|imported|missing"
// @Param       sort           query  string  false  "updated_desc|title_asc|air_date_desc"
// @Param       limit          query  int     false  "Page size (≤200)"
// @Param       cursor         query  string  false  "Opaque keyset cursor"
// @Param       q              query  string  false  "Substring (title / slug)"
// @Param       monitored      query  string  false  "true|false (tri-state)"
// @Param       networks       query  string  false  "Pipe-separated network names"
// @Success     200  {object}  dto.SeriesCacheList
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Failure     404  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series [get]
func (h *GlobalCatalogHandler) List(c *gin.Context) {
	name := c.Query("instance")
	if name == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "instance query param required"})
		return
	}
	if h.inner == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "catalog handler not wired"})
		return
	}
	// Splice :name into c.Params so the existing handler's c.Param("name")
	// lookup works without code duplication. gin.Params are scoped to this
	// request.
	c.Params = append(c.Params, gin.Param{Key: "name", Value: name})
	h.inner.ListSeriesCache(c)
}

// Networks handles GET /api/v1/series/networks.
//
// @Summary     Distinct networks for an instance's series-cache (global)
// @Tags        series
// @Produce     json
// @Param       instance  query  string  true  "Instance name"
// @Success     200       {object}  dto.SeriesCacheNetworksList
// @Failure     400       {object}  dto.ErrorResponse
// @Failure     401       {object}  dto.ErrorResponse
// @Failure     404       {object}  dto.ErrorResponse
// @Failure     500       {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/networks [get]
func (h *GlobalCatalogHandler) Networks(c *gin.Context) {
	name := c.Query("instance")
	if name == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "instance query param required"})
		return
	}
	if h.inner == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "catalog handler not wired"})
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "name", Value: name})
	h.inner.ListSeriesCacheNetworks(c)
}
