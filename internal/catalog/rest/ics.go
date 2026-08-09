package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/icsfeed"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// tokenRejectedBody is the single opaque body for every token rejection —
// no detail on WHICH check failed (do not leak signature vs epoch vs shape).
const tokenRejectedBody = "invalid or revoked calendar token"

// ICSService is the narrow port the handler depends on. Production:
// *icsfeed.UseCase.
type ICSService interface {
	Render(ctx context.Context, token string) (string, error)
	Mint(ctx context.Context, scope string) (icsfeed.Minted, error)
	Revoke(ctx context.Context) (int64, error)
}

// ICSHandler serves the ICS calendar subscription feed (public consume +
// guarded mint/revoke).
type ICSHandler struct {
	svc    ICSService
	logger *slog.Logger
}

func NewICSHandler(svc ICSService, logger *slog.Logger) *ICSHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ICSHandler{svc: svc, logger: logger}
}

type icsTokenResponse struct {
	ICSURL    string `json:"ics_url" example:"https://sf.arr.morbo.dev/api/v1/calendar.ics?token=eyJ...abc"`
	WebcalURL string `json:"webcal_url" example:"webcal://sf.arr.morbo.dev/api/v1/calendar.ics?token=eyJ...abc"`
	Scope     string `json:"scope" example:"all"`
}

type icsRevokeResponse struct {
	Epoch int64 `json:"epoch" example:"1"`
}

// Consume handles GET /api/v1/calendar.ics — PUBLIC (token-authenticated).
//
// @Summary     Subscribe to the release calendar (iCalendar feed)
// @Description Public, token-authenticated iCalendar (RFC 5545) feed of the
// @Description release calendar. The signed token carries the scope and a
// @Description revocation epoch; a bad or revoked token returns 401. ICS
// @Description clients (Google/Apple Calendar) subscribe by URL — no cookies.
// @Tags        calendar
// @Produce     text/calendar
// @Param       token query string true "signed subscription token (from /calendar.ics/token)"
// @Success     200 {string} string "iCalendar document"
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Router      /calendar.ics [get]
func (h *ICSHandler) Consume(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: tokenRejectedBody})
		return
	}
	body, err := h.svc.Render(c.Request.Context(), token)
	if err != nil {
		if errors.Is(err, icsfeed.ErrRevoked) {
			// Deliberately do NOT log the token value — treat as a secret.
			c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: tokenRejectedBody})
			return
		}
		h.logger.ErrorContext(c.Request.Context(), "ics_render_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "calendar feed unavailable"})
		return
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, "text/calendar; charset=utf-8", []byte(body))
}

// Mint handles GET /api/v1/calendar.ics/token — GUARDED.
//
// @Summary     Mint a calendar subscription token
// @Description Mints a signed subscription token at the current revocation
// @Description epoch and returns the absolute .ics URL plus a webcal:// URL
// @Description for one-click subscription. Scope defaults to all.
// @Tags        calendar
// @Produce     json
// @Param       scope query string false "library|followed|all (default all)"
// @Success     200 {object} icsTokenResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /calendar.ics/token [get]
func (h *ICSHandler) Mint(c *gin.Context) {
	m, err := h.svc.Mint(c.Request.Context(), c.Query("scope"))
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "ics_mint_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "mint failed"})
		return
	}
	scheme, host := requestScheme(c), requestHost(c)
	q := "/api/v1/calendar.ics?token=" + url.QueryEscape(m.Token)
	c.JSON(http.StatusOK, icsTokenResponse{
		ICSURL:    scheme + "://" + host + q,
		WebcalURL: "webcal://" + host + q,
		Scope:     m.Scope,
	})
}

// Revoke handles POST /api/v1/calendar.ics/revoke — GUARDED.
//
// @Summary     Revoke all calendar subscription tokens
// @Description Bumps the revocation epoch, invalidating every previously
// @Description minted subscription URL. Returns the new epoch. Does NOT
// @Description affect browser sessions (separate epoch).
// @Tags        calendar
// @Produce     json
// @Success     200 {object} icsRevokeResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /calendar.ics/revoke [post]
func (h *ICSHandler) Revoke(c *gin.Context) {
	ep, err := h.svc.Revoke(c.Request.Context())
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "ics_revoke_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "revoke failed"})
		return
	}
	c.JSON(http.StatusOK, icsRevokeResponse{Epoch: ep})
}

// requestScheme resolves the public scheme for building absolute URLs from
// the operator's authenticated mint request. Trusts X-Forwarded-Proto
// (ingress), then TLS, else http.
func requestScheme(c *gin.Context) string {
	if p := c.GetHeader("X-Forwarded-Proto"); p != "" {
		return p
	}
	if c.Request.TLS != nil {
		return "https"
	}
	return "http"
}

// requestHost resolves the public host. Trusts X-Forwarded-Host (ingress),
// else the request Host header.
func requestHost(c *gin.Context) string {
	if h := c.GetHeader("X-Forwarded-Host"); h != "" {
		return h
	}
	return c.Request.Host
}
