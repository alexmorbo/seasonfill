package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// QbitDiscoverHandler — GET /api/v1/instances/{name}/discover/qbit.
// Calls the arr's /api/v3/downloadclient, filters for the first
// QBittorrent entry (preferring enabled ones), and returns its
// host/port/username/category for the operator to pre-fill the qBit
// settings form. Password is intentionally NOT returned: the arr
// redacts it server-side and we never have access to it.
//
// ADR-0023 F2: both Sonarr AND Radarr instances are supported. `reg` is
// the sonarr-scoped registry; `radarrLookup` is the reload-aware radarr
// instance map (same source the A1 radarr-webhook registry projection
// reads). A name is resolved sonarr-first, then radarr, then 404.
type QbitDiscoverHandler struct {
	reg catalogrest.InstanceRegistry
	// radarrLookup is nil-OK — minimal/test wirings leave it unset and the
	// handler then behaves exactly as it did pre-F2 (sonarr-only; radarr
	// names fall through to "unknown instance"). Never panics on nil.
	radarrLookup catalogrest.RadarrConfigLookup
	logger       *slog.Logger
}

func NewQbitDiscoverHandler(
	reg catalogrest.InstanceRegistry,
	radarrLookup catalogrest.RadarrConfigLookup,
	logger *slog.Logger,
) *QbitDiscoverHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &QbitDiscoverHandler{reg: reg, radarrLookup: radarrLookup, logger: logger}
}

// Discover handles GET /api/v1/instances/:name/discover/qbit.
//
// @Summary     Discover qBit settings from a Sonarr or Radarr instance
// @Description Calls the arr's /api/v3/downloadclient and returns the
// @Description first ENABLED QBittorrent download client (falling back
// @Description to the first one regardless of Enable). Surfaces the
// @Description client `name`, composed `url` (http://host:port), `username`
// @Description and `category`. Password is never returned — the arr
// @Description redacts it server-side; the operator re-enters it into
// @Description the qBit settings form. The instance name is resolved
// @Description against the Sonarr registry first, then the Radarr one.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.QbitDiscoverDTO
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse  "unknown instance OR no qBit configured in the arr"
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/discover/qbit [get]
func (h *QbitDiscoverHandler) Discover(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	// Sonarr first — the pre-F2 path, behaviour unchanged.
	if inst, ok := h.reg.Snapshot()[name]; ok && inst.Client != nil {
		// Use a type-assertion to reach the concrete *sonarr.Client.
		// catalogrest.InstanceRegistry exposes ports.SonarrClient, but
		// ListDownloadClients lives on the concrete type — it is not added
		// to the ports interface because no application use case needs it
		// (handler-only surface).
		concrete, ok := inst.Client.(*sonarr.Client)
		if !ok {
			writeInternalError(c, h.logger, "qbit_discover_client_type_mismatch",
				errors.New("instance client is not *sonarr.Client"),
				slog.String("instance", name))
			return
		}
		clients, err := concrete.ListDownloadClients(ctx)
		if err != nil {
			h.writeUpstreamError(c, "sonarr", name, err)
			return
		}
		h.respond(c, sonarrCandidates(clients))
		return
	}

	// Radarr fallback. nil lookup (minimal/test wirings) → skip, 404 below.
	if h.radarrLookup != nil {
		if inst, ok := h.radarrLookup.Load()[name]; ok && inst.Client != nil {
			// Same rationale as the sonarr assertion: ports.RadarrClient
			// deliberately does NOT declare ListDownloadClients.
			concrete, ok := inst.Client.(*radarr.Client)
			if !ok {
				writeInternalError(c, h.logger, "qbit_discover_client_type_mismatch",
					errors.New("instance client is not *radarr.Client"),
					slog.String("instance", name))
				return
			}
			clients, err := concrete.ListDownloadClients(ctx)
			if err != nil {
				h.writeUpstreamError(c, "radarr", name, err)
				return
			}
			h.respond(c, radarrCandidates(clients))
			return
		}
	}

	c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
}

// respond picks the qBit candidate and writes the 200 or the arr-neutral
// NO_QBIT_FOUND 404. Shared by both arr branches so the response shape and
// the error code cannot drift between them.
func (h *QbitDiscoverHandler) respond(c *gin.Context, candidates []qbitCandidate) {
	picked, found := pickQbitClient(candidates)
	if !found {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{
			Error: "no qBittorrent download client configured in this instance",
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

// writeUpstreamError maps a download-client list failure onto 502. `arr` is
// "sonarr" or "radarr" so the operator sees which upstream actually failed;
// the slog event names stay stable (qbit_discover_*) with arr as an attribute.
func (h *QbitDiscoverHandler) writeUpstreamError(c *gin.Context, arr, name string, err error) {
	ctx := c.Request.Context()
	if errors.Is(err, sharedErrors.ErrInstanceUnauthorized) {
		h.logger.WarnContext(ctx, "qbit_discover_upstream_unauthorized",
			slog.String("instance", name), slog.String("arr", arr),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: arr + " unauthorized"})
		return
	}
	h.logger.ErrorContext(ctx, "qbit_discover_list_failed",
		slog.String("instance", name), slog.String("arr", arr),
		slog.String("error", err.Error()))
	c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: arr + " unavailable"})
}

// qbitCandidate is the arr-neutral projection of a download client. Both
// sonarr.DownloadClient and radarr.DownloadClient collapse onto it so the
// picker + the DTO builder exist exactly once (no per-arr duplication).
type qbitCandidate struct {
	Name           string
	Implementation string
	Enable         bool
	Host           string
	Port           int
	Username       string
	Category       string
}

func sonarrCandidates(list []sonarr.DownloadClient) []qbitCandidate {
	out := make([]qbitCandidate, 0, len(list))
	for _, dc := range list {
		out = append(out, qbitCandidate{
			Name: dc.Name, Implementation: dc.Implementation, Enable: dc.Enable,
			Host: dc.Host, Port: dc.Port, Username: dc.Username, Category: dc.Category,
		})
	}
	return out
}

func radarrCandidates(list []radarr.DownloadClient) []qbitCandidate {
	out := make([]qbitCandidate, 0, len(list))
	for _, dc := range list {
		out = append(out, qbitCandidate{
			Name: dc.Name, Implementation: dc.Implementation, Enable: dc.Enable,
			Host: dc.Host, Port: dc.Port, Username: dc.Username, Category: dc.Category,
		})
	}
	return out
}

// pickQbitClient returns the first QBittorrent download client,
// preferring Enable=true. Lowercase comparison defends against
// arr version drift. Returns found=false when no matches exist.
func pickQbitClient(list []qbitCandidate) (qbitCandidate, bool) {
	var firstAny *qbitCandidate
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
	return qbitCandidate{}, false
}

// buildQbitURL constructs http://host:port. Concerns §1 explains why
// we do NOT attempt to infer https — the arr download-client field
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
