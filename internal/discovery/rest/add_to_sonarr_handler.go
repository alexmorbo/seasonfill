// Package rest — add_to_sonarr_handler.go ships POST
// /api/v1/discovery/add-to-sonarr (story 520 N-4c). Decodes the body,
// dispatches to the use case, mirrors the F-2c envelope via
// c.Error(err) so ErrorResponseMiddleware emits the typed slug.
package rest

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

const addToSonarrBodyLimit = 4 << 10 // 4 KiB

// addToSonarrRequest is the wire shape decoded off the JSON body.
//
// MonitoredSeasons (story 524 N-4 per-season picker) — when non-empty,
// the use case calls Sonarr's lookup endpoint to discover the full
// season list and stamps explicit per-season monitored flags.
type addToSonarrRequest struct {
	InstanceName     string `json:"instance_name"`
	TVDBID           int    `json:"tvdb_id"`
	QualityProfileID int    `json:"quality_profile_id"`
	RootFolderPath   string `json:"root_folder_path"`
	Monitored        *bool  `json:"monitored,omitempty"`
	MonitorMode      string `json:"monitor_mode,omitempty"`
	SearchOnAdd      bool   `json:"search_on_add,omitempty"`
	MonitoredSeasons []int  `json:"monitored_seasons,omitempty"`
}

type addToSonarrResponse struct {
	SonarrSeriesID int    `json:"sonarr_series_id"`
	InstanceName   string `json:"instance_name"`
	UserTagLabel   string `json:"user_tag_label"`
	UserTagID      int    `json:"user_tag_id"`
}

// AddToSonarrHandler owns POST /api/v1/discovery/add-to-sonarr.
type AddToSonarrHandler struct {
	uc  *discoapp.AddToSonarrUseCase
	log *slog.Logger
}

// NewAddToSonarrHandler panics on nil deps — init-time bug. The
// logger MUST already carry the "discovery" domain tag (wiring uses
// sharedports.DomainLogger).
func NewAddToSonarrHandler(uc *discoapp.AddToSonarrUseCase, log *slog.Logger) *AddToSonarrHandler {
	if uc == nil {
		panic("NewAddToSonarrHandler: uc required")
	}
	if log == nil {
		panic("NewAddToSonarrHandler: log required")
	}
	return &AddToSonarrHandler{uc: uc, log: log}
}

// Handle is POST /api/v1/discovery/add-to-sonarr.
func (h *AddToSonarrHandler) Handle(c *gin.Context) {
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "content-type must be application/json",
		})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, addToSonarrBodyLimit)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	var body addToSonarrRequest
	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_request",
				"message": "empty body",
			})
			return
		}
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "malformed body",
		})
		return
	}
	if strings.TrimSpace(body.InstanceName) == "" || body.TVDBID <= 0 ||
		body.QualityProfileID <= 0 || strings.TrimSpace(body.RootFolderPath) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "instance_name, tvdb_id, quality_profile_id, root_folder_path required",
		})
		return
	}
	monitored := true
	if body.Monitored != nil {
		monitored = *body.Monitored
	}
	username := c.GetString(middleware.UsernameContextKey)
	switch username {
	case "api-key", "local", "anonymous":
		username = ""
	}
	req := discoapp.AddRequest{
		InstanceName:     domain.InstanceName(strings.TrimSpace(body.InstanceName)),
		TVDBID:           body.TVDBID,
		QualityProfileID: body.QualityProfileID,
		RootFolderPath:   strings.TrimSpace(body.RootFolderPath),
		Monitored:        monitored,
		MonitorMode:      body.MonitorMode,
		SearchOnAdd:      body.SearchOnAdd,
		Username:         username,
		MonitoredSeasons: body.MonitoredSeasons,
	}
	res, err := h.uc.Add(c.Request.Context(), req)
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	c.JSON(http.StatusOK, addToSonarrResponse{
		SonarrSeriesID: res.SonarrSeriesID,
		InstanceName:   string(res.InstanceName),
		UserTagLabel:   res.UserTagLabel,
		UserTagID:      res.UserTagID,
	})
}
