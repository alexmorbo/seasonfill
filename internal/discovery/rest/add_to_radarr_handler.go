// Package rest — add_to_radarr_handler.go ships POST
// /api/v1/discovery/add-to-radarr (Ф6-R-6a). Mirror of add-to-sonarr: decodes
// the body, dispatches to AddToRadarrUseCase, mirrors the F-2c envelope via
// c.Error(err) so ErrorResponseMiddleware emits the typed slug. NO swagger
// annotation — matches the add-to-sonarr sibling (/discovery/* is hand-authored).
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
)

const addToRadarrBodyLimit = 4 << 10 // 4 KiB

// addToRadarrRequest is the wire shape decoded off the JSON body.
type addToRadarrRequest struct {
	InstanceName        string `json:"instance_name"`
	TMDBID              int    `json:"tmdb_id"`
	QualityProfileID    int    `json:"quality_profile_id"`
	RootFolderPath      string `json:"root_folder_path"`
	Monitored           *bool  `json:"monitored,omitempty"`
	MinimumAvailability string `json:"minimum_availability,omitempty"`
	SearchOnAdd         bool   `json:"search_on_add,omitempty"`
}

type addToRadarrResponse struct {
	RadarrMovieID int    `json:"radarr_movie_id"`
	InstanceName  string `json:"instance_name"`
	AlreadyAdded  bool   `json:"already_added"`
}

// AddToRadarrHandler owns POST /api/v1/discovery/add-to-radarr.
type AddToRadarrHandler struct {
	uc  *discoapp.AddToRadarrUseCase
	log *slog.Logger
}

// NewAddToRadarrHandler panics on nil deps — init-time bug. The logger MUST
// already carry the "discovery" domain tag (wiring uses sharedports.DomainLogger).
func NewAddToRadarrHandler(uc *discoapp.AddToRadarrUseCase, log *slog.Logger) *AddToRadarrHandler {
	if uc == nil {
		panic("NewAddToRadarrHandler: uc required")
	}
	if log == nil {
		panic("NewAddToRadarrHandler: log required")
	}
	return &AddToRadarrHandler{uc: uc, log: log}
}

// Handle is POST /api/v1/discovery/add-to-radarr.
func (h *AddToRadarrHandler) Handle(c *gin.Context) {
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "content-type must be application/json",
		})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, addToRadarrBodyLimit)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	var body addToRadarrRequest
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
	if strings.TrimSpace(body.InstanceName) == "" || body.TMDBID <= 0 ||
		body.QualityProfileID <= 0 || strings.TrimSpace(body.RootFolderPath) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "instance_name, tmdb_id, quality_profile_id, root_folder_path required",
		})
		return
	}
	monitored := true
	if body.Monitored != nil {
		monitored = *body.Monitored
	}
	res, err := h.uc.Add(c.Request.Context(), discoapp.AddMovieRequest{
		InstanceName:        domain.InstanceName(strings.TrimSpace(body.InstanceName)),
		TMDBID:              body.TMDBID,
		QualityProfileID:    body.QualityProfileID,
		RootFolderPath:      strings.TrimSpace(body.RootFolderPath),
		Monitored:           monitored,
		MinimumAvailability: strings.TrimSpace(body.MinimumAvailability),
		SearchOnAdd:         body.SearchOnAdd,
	})
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	c.JSON(http.StatusOK, addToRadarrResponse{
		RadarrMovieID: res.RadarrMovieID,
		InstanceName:  string(res.InstanceName),
		AlreadyAdded:  res.AlreadyAdded,
	})
}
