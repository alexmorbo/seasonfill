package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/moviecollection"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// collectionCanonReader reads the collection header row. Production impl:
// *enrichpersistence.MovieCollectionsRepository.GetByTMDBCollectionID. Narrow
// port keeps catalog/rest decoupled from enrichment persistence.
type collectionCanonReader interface {
	GetByTMDBCollectionID(ctx context.Context, tmdbCollectionID int) (movie.CollectionCanon, error)
}

// MovieCollectionsHandler serves the three collection routes (Ф6-R-6a).
// defaultInstance returns the sole registered radarr instance name when exactly
// one exists (used to resolve membership when ?instance= is omitted); ""
// otherwise. resolver rewrites raw TMDB poster paths (header + each part) to
// sha256 media hashes (nil-OK → raw paths flow through, pre-U-2 behavior).
type MovieCollectionsHandler struct {
	reader          ports.MovieCollectionsReader
	canon           collectionCanonReader
	addAll          *moviecollection.AddMissingUseCase
	monitor         *moviecollection.RadarrMonitorUseCase
	defaultInstance func() string
	resolver        *media.Resolver
	log             *slog.Logger
}

func NewMovieCollectionsHandler(
	reader ports.MovieCollectionsReader,
	canon collectionCanonReader,
	addAll *moviecollection.AddMissingUseCase,
	monitor *moviecollection.RadarrMonitorUseCase,
	defaultInstance func() string,
	resolver *media.Resolver,
	log *slog.Logger,
) *MovieCollectionsHandler {
	if log == nil {
		log = slog.Default()
	}
	return &MovieCollectionsHandler{
		reader:          reader,
		canon:           canon,
		addAll:          addAll,
		monitor:         monitor,
		defaultInstance: defaultInstance,
		resolver:        resolver,
		log:             log,
	}
}

// Get handles GET /api/v1/collections/:tmdb_collection_id.
//
// @Summary     TMDB franchise collection detail
// @Description Collection header + member parts with per-instance library
// @Description membership. instance resolves membership; when omitted and
// @Description exactly one radarr instance is registered it is used, else 400.
// @Description lang localizes part titles (canon fallback).
// @Tags        collections
// @Produce     json
// @Param       tmdb_collection_id path int    true  "TMDB collection id"
// @Param       instance           query string false "radarr instance for membership"
// @Param       lang               query string false "BCP-47 language tag"
// @Success     200 {object} dto.MovieCollectionDetail
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /collections/{tmdb_collection_id} [get]
func (h *MovieCollectionsHandler) Get(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	instance := strings.TrimSpace(c.Query("instance"))
	if instance == "" && h.defaultInstance != nil {
		instance = h.defaultInstance()
	}
	if instance == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "instance query param required (no single default radarr instance)", Code: "BAD_REQUEST"})
		return
	}
	lang := strings.TrimSpace(c.Query("lang"))
	ctx := c.Request.Context()
	canon, err := h.canon.GetByTMDBCollectionID(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "collection_not_found", Code: "COLLECTION_NOT_FOUND"})
			return
		}
		h.log.ErrorContext(ctx, "collection_get_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "collection unavailable"})
		return
	}
	parts, err := h.reader.ListPartsWithMembership(ctx, id, instance, lang)
	if err != nil {
		h.log.ErrorContext(ctx, "collection_parts_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "collection unavailable"})
		return
	}
	headerPoster := canon.PosterAsset
	if h.resolver != nil {
		if hash := h.resolver.Resolve(ctx, canon.PosterAsset, "w342", "poster_w342"); hash != nil {
			headerPoster = hash
		}
	}
	out := dto.MovieCollectionDetail{
		TMDBCollectionID: canon.TMDBCollectionID, Name: canon.Name, Overview: canon.Overview,
		Poster: headerPoster, RadarrMonitored: canon.RadarrMonitored, Instance: instance,
	}
	for _, p := range parts {
		partPoster := p.Poster
		if h.resolver != nil {
			if hash := h.resolver.Resolve(ctx, p.Poster, "w342", "poster_w342"); hash != nil {
				partPoster = hash
			}
		}
		out.Parts = append(out.Parts, dto.MovieCollectionPartDTO{
			MovieID: p.MovieID, TMDBID: p.TMDBID, Title: p.Title, Year: p.Year,
			InLibrary: p.InLibrary, Poster: partPoster,
		})
	}
	c.JSON(http.StatusOK, out)
}

// AddAllMissing handles POST /api/v1/collections/:tmdb_collection_id/add-all-missing.
//
// @Summary     Add every missing collection part to Radarr
// @Tags        collections
// @Accept      json
// @Produce     json
// @Param       tmdb_collection_id path int true "TMDB collection id"
// @Param       body body dto.MovieCollectionAddAllRequest true "instance + quality/root knobs"
// @Success     200 {object} dto.MovieCollectionAddAllResponse
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /collections/{tmdb_collection_id}/add-all-missing [post]
func (h *MovieCollectionsHandler) AddAllMissing(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	var body dto.MovieCollectionAddAllRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "malformed body", Code: "BAD_REQUEST"})
		return
	}
	if strings.TrimSpace(body.InstanceName) == "" || body.QualityProfileID <= 0 || strings.TrimSpace(body.RootFolderPath) == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "instance_name, quality_profile_id, root_folder_path required", Code: "BAD_REQUEST"})
		return
	}
	sum, err := h.addAll.AddAllMissing(c.Request.Context(), moviecollection.AddMissingRequest{
		InstanceName:        domain.InstanceName(strings.TrimSpace(body.InstanceName)),
		TMDBCollectionID:    id,
		QualityProfileID:    body.QualityProfileID,
		RootFolderPath:      strings.TrimSpace(body.RootFolderPath),
		Monitored:           body.Monitored,
		MinimumAvailability: strings.TrimSpace(body.MinimumAvailability),
		SearchOnAdd:         body.SearchOnAdd,
	})
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	resp := dto.MovieCollectionAddAllResponse{Requested: sum.Requested, Added: sum.Added, AlreadyPresent: sum.AlreadyPresent, Failed: sum.Failed}
	for _, p := range sum.Parts {
		resp.Parts = append(resp.Parts, dto.MovieCollectionAddPartDTO{
			TMDBID: p.TMDBID, Title: p.Title, RadarrMovieID: p.RadarrMovieID,
			AlreadyAdded: p.AlreadyAdded, Skipped: p.Skipped, Error: p.Err,
		})
	}
	c.JSON(http.StatusOK, resp)
}

// Monitor handles PUT /api/v1/collections/:tmdb_collection_id/monitor.
//
// @Summary     Enable Radarr native monitor for a collection
// @Tags        collections
// @Accept      json
// @Produce     json
// @Param       tmdb_collection_id path int true "TMDB collection id"
// @Param       body body dto.MovieCollectionMonitorRequest true "instance"
// @Success     204
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /collections/{tmdb_collection_id}/monitor [put]
func (h *MovieCollectionsHandler) Monitor(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	var body dto.MovieCollectionMonitorRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "malformed body", Code: "BAD_REQUEST"})
		return
	}
	if strings.TrimSpace(body.InstanceName) == "" {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "instance_name required", Code: "BAD_REQUEST"})
		return
	}
	err := h.monitor.EnableNativeMonitor(c.Request.Context(), moviecollection.EnableMonitorRequest{
		InstanceName:     domain.InstanceName(strings.TrimSpace(body.InstanceName)),
		TMDBCollectionID: id,
	})
	if err != nil {
		if errors.Is(err, moviecollection.ErrRadarrCollectionNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "radarr_collection_not_found", Code: "RADARR_COLLECTION_NOT_FOUND"})
			return
		}
		_ = c.Error(err) // InstanceNotFoundError/ErrNotFound → typed middleware
		c.Abort()
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *MovieCollectionsHandler) parseID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("tmdb_collection_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb_collection_id", Code: "BAD_REQUEST"})
		return 0, false
	}
	return id, true
}
