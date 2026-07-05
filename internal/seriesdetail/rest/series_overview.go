package rest

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeriesOverviewHandler serves the overview subset (Story 529).
// Per-instance route is never registered — reached only via the global
// wrapper splicing :name + :id. Matches the SeriesCastHandler pattern.
type SeriesOverviewHandler struct {
	composer *seriesdetail.Composer
	logger   *slog.Logger
}

func NewSeriesOverviewHandler(composer *seriesdetail.Composer, logger *slog.Logger) *SeriesOverviewHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesOverviewHandler{composer: composer, logger: logger}
}

func (h *SeriesOverviewHandler) Get(c *gin.Context) {
	name := c.Param("name")
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	sonarrID := domain.SonarrSeriesID(parsedID)
	lang := strings.TrimSpace(c.Query("lang"))

	ctx := c.Request.Context()
	ov, err := h.composer.GetOverview(ctx, domain.InstanceName(name), sonarrID, lang)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toSeriesOverviewResponse(ov))
}

func toSeriesOverviewResponse(o *seriesdetail.Overview) dto.SeriesOverviewResponse {
	resp := dto.SeriesOverviewResponse{
		Instance:       o.Instance,
		SonarrSeriesID: o.SonarrSeriesID,
		SeriesID:       o.SeriesID,
		Lang:           o.Lang,
		Overview: dto.OverviewAside{
			Overview:   o.Description,
			Language:   o.DescriptionLanguage,
			Keywords:   make([]dto.TaxonomyChip, 0, len(o.Keywords)),
			Awards:     o.Awards,
			RTRating:   o.RTRating,
			Metacritic: o.Metacritic,
		},
		Degraded: append([]string{}, o.Degraded...),
	}
	for _, k := range o.Keywords {
		resp.Overview.Keywords = append(resp.Overview.Keywords, dto.TaxonomyChip{
			ID: k.ID, Name: k.Name, Language: k.Language,
		})
	}
	return resp
}
