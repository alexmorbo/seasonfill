package rest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	appta "github.com/alexmorbo/seasonfill/internal/torrentaction/app"
)

// Handler serves the three instance-scoped torrent action endpoints
// (ADR-0013 Q2, audit F-16 — actions ONLY on the instance path):
//
//	POST /api/v1/instances/:name/torrents/:hash/pause
//	POST /api/v1/instances/:name/torrents/:hash/resume
//	POST /api/v1/instances/:name/torrents/:hash/recheck
type Handler struct {
	uc     *appta.UseCase
	logger *slog.Logger
}

// NewHandler wires the handler. logger defaults to slog.Default when nil.
func NewHandler(uc *appta.UseCase, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "http")
	}
	return &Handler{uc: uc, logger: logger}
}

// Pause godoc
//
//	@Summary		Pause one of our torrents
//	@Description	Pauses a torrent grabbed through seasonfill. Hash-first guard: a
//	@Description	hash outside our grab_records returns 404. Idempotent — pausing an
//	@Description	already-paused torrent is a 200 no-op.
//	@Tags			torrents
//	@Produce		json
//	@Param			name	path		string	true	"Instance name"
//	@Param			hash	path		string	true	"Torrent info-hash (40-char hex)"
//	@Success		200		{object}	dto.TorrentActionResponse
//	@Failure		400		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		502		{object}	dto.ErrorResponse
//	@Router			/instances/{name}/torrents/{hash}/pause [post]
func (h *Handler) Pause(c *gin.Context) { h.do(c, appta.ActionPause) }

// Resume godoc
//
//	@Summary		Resume one of our torrents
//	@Description	Resumes a torrent grabbed through seasonfill (hash-first guard).
//	@Tags			torrents
//	@Produce		json
//	@Param			name	path		string	true	"Instance name"
//	@Param			hash	path		string	true	"Torrent info-hash (40-char hex)"
//	@Success		200		{object}	dto.TorrentActionResponse
//	@Failure		400		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		502		{object}	dto.ErrorResponse
//	@Router			/instances/{name}/torrents/{hash}/resume [post]
func (h *Handler) Resume(c *gin.Context) { h.do(c, appta.ActionResume) }

// Recheck godoc
//
//	@Summary		Recheck one of our torrents
//	@Description	Triggers a hash recheck of a torrent grabbed through seasonfill
//	@Description	(hash-first guard).
//	@Tags			torrents
//	@Produce		json
//	@Param			name	path		string	true	"Instance name"
//	@Param			hash	path		string	true	"Torrent info-hash (40-char hex)"
//	@Success		200		{object}	dto.TorrentActionResponse
//	@Failure		400		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		502		{object}	dto.ErrorResponse
//	@Router			/instances/{name}/torrents/{hash}/recheck [post]
func (h *Handler) Recheck(c *gin.Context) { h.do(c, appta.ActionRecheck) }

func (h *Handler) do(c *gin.Context, action appta.Action) {
	name := c.Param("name")
	rawHash := c.Param("hash")

	// Validate + normalise the hash. Invalid shape -> 400 (never reaches
	// the grab lookup). NewQbitHash lowercases + regex-checks 40-hex.
	hash, err := shareddomain.NewQbitHash(rawHash)
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid torrent hash"})
		return
	}

	actor := c.GetString(middleware.UsernameContextKey)

	ctx := c.Request.Context()
	err = h.uc.Do(ctx, appta.Input{
		Instance: shareddomain.InstanceName(name),
		Hash:     string(hash),
		Action:   action,
		Actor:    actor,
	})
	switch {
	case err == nil:
		c.JSON(http.StatusOK, dto.TorrentActionResponse{
			Status: "ok",
			Action: string(action),
			Hash:   string(hash),
		})
	case errors.Is(err, sharedErrors.ErrInstanceNetwork):
		h.logger.WarnContext(ctx, "torrent_action_qbit_unreachable",
			slog.String("instance", name),
			slog.String("hash", string(hash)),
			slog.String("action", string(action)),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "qbit unreachable"})
	case errors.Is(err, ports.ErrNotFound):
		_ = c.Error(err) // middleware -> 404 not_found
	default:
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal error"})
	}
}
