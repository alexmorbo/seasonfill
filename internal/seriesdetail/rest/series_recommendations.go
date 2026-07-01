package rest

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeriesRecommendationsHandler serves the recommendations subset (Story 530).
// Per-instance route is never registered — reached only via the global
// wrapper splicing :name + :id. Matches the SeriesOverviewHandler pattern.
type SeriesRecommendationsHandler struct {
	composer *seriesdetail.Composer
	logger   *slog.Logger
}

func NewSeriesRecommendationsHandler(composer *seriesdetail.Composer, logger *slog.Logger) *SeriesRecommendationsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesRecommendationsHandler{composer: composer, logger: logger}
}

func (h *SeriesRecommendationsHandler) Get(c *gin.Context) {
	name := c.Param("name")
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	sonarrID := domain.SonarrSeriesID(parsedID)

	limit, ok := parseRecLimit(c)
	if !ok {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid limit"})
		return
	}
	offset, ok := parseRecOffset(c)
	if !ok {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid offset"})
		return
	}

	// Story 565 (B-recs-lang) — pass ?lang= through to the composer so
	// recommendation card titles come out localised. Empty / invalid
	// values are normalised by the composer's resolveLang.
	lang := c.Query("lang")

	ctx := c.Request.Context()
	rec, err := h.composer.GetRecommendations(ctx, domain.InstanceName(name), sonarrID, lang, limit, offset)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toSeriesRecommendationsResponse(rec, limit, offset))
}

// parseRecLimit reads ?limit=N. Empty → default. Negative / non-int /
// out-of-range → (0,false). The composer clamps internally but the
// handler returns 400 on a bad value so callers learn the contract.
func parseRecLimit(c *gin.Context) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return seriesdetail.RecommendationsLimitDefault, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if n < seriesdetail.RecommendationsLimitMin || n > seriesdetail.RecommendationsLimitMax {
		return 0, false
	}
	return n, true
}

func parseRecOffset(c *gin.Context) (int, bool) {
	raw := c.Query("offset")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func toSeriesRecommendationsResponse(r *seriesdetail.Recommendations, limit, offset int) dto.SeriesRecommendationsResponse {
	resp := dto.SeriesRecommendationsResponse{
		Instance:       r.Instance,
		SonarrSeriesID: r.SonarrSeriesID,
		SeriesID:       r.SeriesID,
		Items:          mapRecommendations(r.Items),
		TotalCount:     r.TotalCount,
		HasMore:        r.HasMore,
		Limit:          limit,
		Offset:         offset,
		Degraded:       append([]string{}, r.Degraded...),
	}
	if resp.Items == nil {
		resp.Items = []dto.Recommendation{}
	}
	return resp
}
