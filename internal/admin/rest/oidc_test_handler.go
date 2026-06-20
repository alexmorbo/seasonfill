package rest

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type oidcTestRequest struct {
	Issuer   *string  `json:"issuer"`
	ClientID *string  `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

// OIDCTestHandler serves POST /api/v1/auth/oidc/test (admin-guarded).
// Body fields override the runtime snapshot; empty fields fall back to
// the snapshot values. Response is always HTTP 200 with the four sub-check
// results; 503 only on system-level failure (unreachable before any check).
type OIDCTestHandler struct {
	rt     *middleware.AuthRuntimePointer
	uc     *auth.OIDCTestUseCase
	logger *slog.Logger
}

// NewOIDCTestHandler creates an OIDCTestHandler.
func NewOIDCTestHandler(rt *middleware.AuthRuntimePointer, logger *slog.Logger) *OIDCTestHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &OIDCTestHandler{rt: rt, uc: auth.NewOIDCTestUseCase(), logger: logger}
}

// Test runs the four reachability checks against the OIDC provider.
//
// @Summary     OIDC reachability test
// @Tags        auth
// @Accept      json
// @Produce     json
// @Success     200  {object}  auth.OIDCTestResult
// @Router      /auth/oidc/test [post]
func (h *OIDCTestHandler) Test(c *gin.Context) {
	var body oidcTestRequest
	_ = c.ShouldBindJSON(&body) // empty body is OK — fall back to snapshot

	snap := middleware.OIDCRuntime{}
	if v := h.rt.Load(); v != nil {
		snap = v.OIDC
	}
	in := auth.OIDCTestInput{
		Issuer:   pickStr(body.Issuer, snap.Issuer),
		ClientID: pickStr(body.ClientID, snap.ClientID),
		Scopes:   pickSlice(body.Scopes, snap.Scopes),
	}
	c.JSON(http.StatusOK, h.uc.Test(c.Request.Context(), in))
}

func pickStr(d *string, fb string) string {
	if d != nil && *d != "" {
		return *d
	}
	return fb
}

func pickSlice(d, fb []string) []string {
	if len(d) > 0 {
		return d
	}
	return fb
}
