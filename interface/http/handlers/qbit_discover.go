package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// QbitDiscoverHandler — GET /api/v1/instances/{name}/discover/qbit.
// Calls Sonarr's /api/v3/downloadclient, filters for the first
// QBittorrent entry (preferring enabled ones), and returns its
// host/port/username/category for the operator to pre-fill the qBit
// settings form. Password is intentionally NOT returned: Sonarr
// redacts it server-side and we never have access to it.
type QbitDiscoverHandler struct {
	reg    catalogrest.InstanceRegistry
	logger *slog.Logger
}

func NewQbitDiscoverHandler(reg catalogrest.InstanceRegistry, logger *slog.Logger) *QbitDiscoverHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &QbitDiscoverHandler{reg: reg, logger: logger}
}

// Discover handles GET /api/v1/instances/:name/discover/qbit.
//
// @Summary     Discover qBit settings from a Sonarr instance
// @Description Calls Sonarr's /api/v3/downloadclient and returns the
// @Description first QBittorrent download client's host/port/username/
// @Description category. Password is never returned — Sonarr redacts
// @Description it server-side; the operator types it themselves into
// @Description the qBit settings form.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.QbitDiscoverDTO
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse  "unknown instance OR no qBit configured in Sonarr"
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/discover/qbit [get]
func (h *QbitDiscoverHandler) Discover(c *gin.Context) {
	name := c.Param("name")
	inst, ok := h.reg.Snapshot()[name]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}

	// Use a type-assertion to reach the concrete *sonarr.Client.
	// The catalogrest.InstanceRegistry exposes ports.SonarrClient, but the new
	// methods (ListDownloadClients) live on the concrete type — they
	// are not added to the ports interface because no application
	// use case needs them (handler-only surface).
	concrete, ok := inst.Client.(*sonarr.Client)
	if !ok {
		writeInternalError(c, h.logger, "qbit_discover_client_type_mismatch",
			errors.New("instance client is not *sonarr.Client"),
			slog.String("instance", name))
		return
	}

	ctx := c.Request.Context()
	clients, err := concrete.ListDownloadClients(ctx)
	if err != nil {
		if errors.Is(err, sharedErrors.ErrInstanceUnauthorized) {
			h.logger.WarnContext(ctx, "qbit_discover_upstream_unauthorized",
				slog.String("instance", name), slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
			return
		}
		h.logger.ErrorContext(ctx, "qbit_discover_list_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	picked, found := pickQbitClient(clients)
	if !found {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{
			Error: "no qBittorrent download client configured in this Sonarr instance",
			Code:  "NO_QBIT_FOUND",
		})
		return
	}

	c.JSON(http.StatusOK, dto.QbitDiscoverDTO{
		Name:     picked.Name,
		URL:      buildQbitURL(picked.Host, picked.Port),
		Username: picked.Username,
		Category: picked.Category,
	})
}

// pickQbitClient returns the first QBittorrent download client,
// preferring Enable=true. Lowercase comparison defends against
// Sonarr version drift. Returns found=false when no matches exist.
func pickQbitClient(list []sonarr.DownloadClient) (sonarr.DownloadClient, bool) {
	var firstAny *sonarr.DownloadClient
	for i, dc := range list {
		if !strings.EqualFold(dc.Implementation, "QBittorrent") {
			continue
		}
		if dc.Enable {
			return dc, true
		}
		if firstAny == nil {
			firstAny = &list[i]
		}
	}
	if firstAny != nil {
		return *firstAny, true
	}
	return sonarr.DownloadClient{}, false
}

// buildQbitURL constructs http://host:port. Concerns §1 explains why
// we do NOT attempt to infer https — Sonarr's download-client field
// schema does not surface a useSsl boolean we can rely on.
func buildQbitURL(host string, port int) string {
	if host == "" {
		return ""
	}
	if port <= 0 {
		return "http://" + host
	}
	return "http://" + host + ":" + strconv.Itoa(port)
}
