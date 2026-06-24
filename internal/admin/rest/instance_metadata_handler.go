package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// InstanceMetadataHandler owns the three N-4b endpoints:
//
//	GET  /api/v1/instances/{name}/quality-profiles
//	GET  /api/v1/instances/{name}/root-folders
//	POST /api/v1/instances/{name}/refresh-metadata
//
// Error envelope is uniform F-2c shape (`{"error": "<slug>",
// "message": "<human>"}`) emitted by ErrorResponseMiddleware off
// c.Error(err); typed errors from the use case carry the slug
// (instance_not_found 404, sonarr_unreachable 502).
type InstanceMetadataHandler struct {
	uc     *authapp.InstanceMetadataUseCase
	logger *slog.Logger
}

// NewInstanceMetadataHandler panics on nil uc — init-time bug.
func NewInstanceMetadataHandler(uc *authapp.InstanceMetadataUseCase, logger *slog.Logger) *InstanceMetadataHandler {
	if uc == nil {
		panic("rest.NewInstanceMetadataHandler: uc must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &InstanceMetadataHandler{uc: uc, logger: logger}
}

// qualityProfileItemDTO is the wire shape per response item. Mirrors
// ports.QualityProfile but uses camelCase for the FE.
type qualityProfileItemDTO struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// rootFolderItemDTO is the wire shape per response item. `accessible`
// is required (defaults to false on older Sonarr instances that omit
// the field; the upstream client surfaces zero-value bool).
type rootFolderItemDTO struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  int64  `json:"free_space"`
}

type qualityProfilesResponse struct {
	Items        []qualityProfileItemDTO `json:"items"`
	RefreshedAt  string                  `json:"refreshed_at"`
	CacheStatus  string                  `json:"cache_status"`
	InstanceName string                  `json:"instance_name"`
}

type rootFoldersResponse struct {
	Items        []rootFolderItemDTO `json:"items"`
	RefreshedAt  string              `json:"refreshed_at"`
	CacheStatus  string              `json:"cache_status"`
	InstanceName string              `json:"instance_name"`
}

type refreshMetadataResponse struct {
	Invalidated bool `json:"invalidated"`
}

// GetQualityProfiles is GET /api/v1/instances/{name}/quality-profiles.
func (h *InstanceMetadataHandler) GetQualityProfiles(c *gin.Context) {
	name := c.Param("name")
	res, err := h.uc.GetQualityProfiles(c.Request.Context(), name)
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	items := make([]qualityProfileItemDTO, 0, len(res.Items))
	for _, qp := range res.Items {
		items = append(items, qualityProfileItemDTO{ID: qp.ID, Name: qp.Name})
	}
	c.JSON(http.StatusOK, qualityProfilesResponse{
		Items:        items,
		RefreshedAt:  res.RefreshedAt.UTC().Format(http.TimeFormat),
		CacheStatus:  res.CacheStatus,
		InstanceName: res.InstanceName,
	})
}

// GetRootFolders is GET /api/v1/instances/{name}/root-folders.
func (h *InstanceMetadataHandler) GetRootFolders(c *gin.Context) {
	name := c.Param("name")
	res, err := h.uc.GetRootFolders(c.Request.Context(), name)
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	items := make([]rootFolderItemDTO, 0, len(res.Items))
	for _, rf := range res.Items {
		items = append(items, rootFolderItemDTO{
			ID: rf.ID, Path: rf.Path,
			Accessible: rf.Accessible, FreeSpace: rf.FreeSpace,
		})
	}
	c.JSON(http.StatusOK, rootFoldersResponse{
		Items:        items,
		RefreshedAt:  res.RefreshedAt.UTC().Format(http.TimeFormat),
		CacheStatus:  res.CacheStatus,
		InstanceName: res.InstanceName,
	})
}

// sonarrLookupSeasonDTO is one season entry in the lookup response.
// Matches the FE picker contract: season_number + episode_count +
// default monitored flag (mirrors Sonarr's default selection).
type sonarrLookupSeasonDTO struct {
	SeasonNumber int  `json:"season_number"`
	EpisodeCount int  `json:"episode_count"`
	Monitored    bool `json:"monitored"`
}

type sonarrLookupResponse struct {
	Items        []sonarrLookupSeasonDTO `json:"items"`
	Title        string                  `json:"title"`
	Year         int                     `json:"year"`
	Overview     string                  `json:"overview"`
	ImageURL     string                  `json:"image_url"`
	TVDBID       int                     `json:"tvdb_id"`
	TMDBID       int                     `json:"tmdb_id"`
	InstanceName string                  `json:"instance_name"`
}

// SonarrLookup is GET /api/v1/instances/{name}/sonarr-lookup?tvdb_id=N.
// Story 524 N-4 per-season picker — returns the seasons preview for the
// requested TVDB id without persisting anything Sonarr-side. 404 when
// Sonarr's metadata provider returns no rows; 502 on Sonarr 5xx /
// network failure. Missing/invalid tvdb_id surfaces 400 invalid_request
// directly (no use-case round-trip).
func (h *InstanceMetadataHandler) SonarrLookup(c *gin.Context) {
	name := c.Param("name")
	raw := c.Query("tvdb_id")
	if raw == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "tvdb_id query parameter required",
		})
		return
	}
	tvdbID, err := strconv.Atoi(raw)
	if err != nil || tvdbID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "tvdb_id must be a positive integer",
		})
		return
	}
	res, err := h.uc.LookupSeries(c.Request.Context(), name, tvdbID)
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	if len(res.Items) == 0 {
		_ = c.Error(errors.Join(
			&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
			ports.ErrNotFound,
		))
		c.Abort()
		return
	}
	first := res.Items[0]
	items := make([]sonarrLookupSeasonDTO, 0, len(first.Seasons))
	for _, s := range first.Seasons {
		items = append(items, sonarrLookupSeasonDTO{
			SeasonNumber: s.SeasonNumber,
			EpisodeCount: s.EpisodeCount,
			Monitored:    s.Monitored,
		})
	}
	c.JSON(http.StatusOK, sonarrLookupResponse{
		Items:        items,
		Title:        first.Title,
		Year:         first.Year,
		Overview:     first.Overview,
		ImageURL:     first.ImageURL,
		TVDBID:       first.TVDBID,
		TMDBID:       first.TMDBID,
		InstanceName: res.InstanceName,
	})
}

// RefreshMetadata is POST /api/v1/instances/{name}/refresh-metadata.
func (h *InstanceMetadataHandler) RefreshMetadata(c *gin.Context) {
	name := c.Param("name")
	if err := h.uc.RefreshMetadata(c.Request.Context(), name); err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	c.JSON(http.StatusOK, refreshMetadataResponse{Invalidated: true})
}
