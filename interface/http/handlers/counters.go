package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// CountersHandler serves the B6 aggregation endpoints.
//
// reg gives us the live instance map so the aggregate handler can
// enumerate every configured Sonarr without an extra DB lookup.
// repo is the dialect-aware bucket aggregator. clock returns the
// current UTC instant; production wires time.Now, tests freeze it.
type CountersHandler struct {
	reg    InstanceRegistry
	repo   ports.CounterRepository
	clock  func() time.Time
	logger *slog.Logger
}

func NewCountersHandler(reg InstanceRegistry, repo ports.CounterRepository, logger *slog.Logger) *CountersHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CountersHandler{
		reg:    reg,
		repo:   repo,
		clock:  func() time.Time { return time.Now().UTC() },
		logger: logger,
	}
}

// WithClock swaps the clock for deterministic handler tests.
func (h *CountersHandler) WithClock(clock func() time.Time) *CountersHandler {
	h.clock = clock
	return h
}

// ForInstance handles GET /api/v1/instances/:name/counters.
//
// @Summary     Per-instance counters and sparkline
// @Description Grabs/imports/fails totals and bucketed sparkline for
// @Description the requested window (24h hourly, 7d/30d daily). Plus
// @Description the 7-day daily-grabs average for above/below copy.
// @Tags        instances
// @Produce     json
// @Param       name   path      string  true   "Instance name"
// @Param       window query     string  false  "24h|7d|30d (default 24h)"  Enums(24h, 7d, 30d)
// @Success     200    {object}  dto.InstanceCountersDTO
// @Failure     400    {object}  dto.ErrorResponse
// @Failure     401    {object}  dto.ErrorResponse
// @Failure     404    {object}  dto.ErrorResponse
// @Failure     500    {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/counters [get]
func (h *CountersHandler) ForInstance(c *gin.Context) {
	name := c.Param("name")
	if _, ok := h.reg.snapshot()[name]; !ok {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}

	window, err := parseCounterWindow(c.Query("window"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	now := h.clock()
	out, err := h.buildInstanceCounters(c, name, window, now)
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "counters_query_failed",
			slog.String("instance", name), slog.String("window", string(window)),
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "counters unavailable"})
		return
	}
	c.JSON(http.StatusOK, out)
}

// Aggregate handles GET /api/v1/counters — one row per instance.
//
// @Summary     Aggregate counters across every instance
// @Description Returns InstanceCountersDTO per configured Sonarr.
// @Tags        counters
// @Produce     json
// @Param       window query     string  false  "24h|7d|30d (default 24h)"  Enums(24h, 7d, 30d)
// @Success     200    {object}  dto.CountersAggregateDTO
// @Failure     400    {object}  dto.ErrorResponse
// @Failure     401    {object}  dto.ErrorResponse
// @Failure     500    {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /counters [get]
func (h *CountersHandler) Aggregate(c *gin.Context) {
	window, err := parseCounterWindow(c.Query("window"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	instMap := h.reg.snapshot()
	names := make([]string, 0, len(instMap))
	for n := range instMap {
		names = append(names, n)
	}
	sort.Strings(names)

	now := h.clock()
	items := make([]dto.InstanceCountersDTO, 0, len(names))
	for _, n := range names {
		row, err := h.buildInstanceCounters(c, n, window, now)
		if err != nil {
			h.logger.WarnContext(c.Request.Context(), "counters_aggregate_item_failed",
				slog.String("instance", n), slog.String("error", err.Error()))
			continue
		}
		items = append(items, row)
	}
	c.JSON(http.StatusOK, dto.CountersAggregateDTO{Items: items})
}

func (h *CountersHandler) buildInstanceCounters(
	c *gin.Context, name string, window ports.CounterWindow, now time.Time,
) (dto.InstanceCountersDTO, error) {
	ctx := c.Request.Context()
	buckets, err := h.repo.BucketCounters(ctx, name, window, now)
	if err != nil {
		return dto.InstanceCountersDTO{}, err
	}
	avg, err := h.repo.AvgGrabsLast7Days(ctx, name, now)
	if err != nil {
		return dto.InstanceCountersDTO{}, err
	}

	out := dto.InstanceCountersDTO{
		InstanceName: name,
		Window:       string(window),
		Sparkline:    make([]dto.CounterBucketDTO, 0, len(buckets)),
		AvgGrabs7d:   avg,
	}
	for _, b := range buckets {
		out.Totals.Grabs += b.Grabs
		out.Totals.Imports += b.Imports
		out.Totals.Fails += b.Fails
		out.Sparkline = append(out.Sparkline, dto.CounterBucketDTO{
			Date:    b.BucketStart,
			Grabs:   b.Grabs,
			Imports: b.Imports,
			Fails:   b.Fails,
		})
	}
	return out, nil
}

// parseCounterWindow validates the ?window= query. Default = 24h so the
// Dashboard's "Сегодня" tile can omit the param.
func parseCounterWindow(raw string) (ports.CounterWindow, error) {
	switch raw {
	case "":
		return ports.CounterWindow24h, nil
	case string(ports.CounterWindow24h):
		return ports.CounterWindow24h, nil
	case string(ports.CounterWindow7d):
		return ports.CounterWindow7d, nil
	case string(ports.CounterWindow30d):
		return ports.CounterWindow30d, nil
	}
	return "", errors.New("invalid window")
}
