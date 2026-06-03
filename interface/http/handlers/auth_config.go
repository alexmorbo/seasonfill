package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// AuthConfigHandler serves GET /api/v1/auth/config — the public
// bootstrap shape the SPA reads on load to decide whether to render
// Login / Logout / banners. Reads from the same AuthRuntime atomic
// the middleware dispatcher uses (single source of truth, no extra
// DB hit).
type AuthConfigHandler struct {
	runtime *middleware.AuthRuntimePointer
}

func NewAuthConfigHandler(ptr *middleware.AuthRuntimePointer) *AuthConfigHandler {
	return &AuthConfigHandler{runtime: ptr}
}

// Get returns {mode, local_bypass}. Public — never gated by RequireAuth.
//
// @Summary     Public auth-mode bootstrap
// @Description Returns the active auth backend (forms|basic|none) and
// @Description whether local-address bypass is enabled. Public endpoint —
// @Description no authentication required.
// @Tags        auth
// @Produce     json
// @Success     200  {object}  dto.AuthConfigDTO
// @Router      /auth/config [get]
func (h *AuthConfigHandler) Get(c *gin.Context) {
	mode := runtime.AuthModeForms
	bypass := false
	if h.runtime != nil {
		if v := h.runtime.Load(); v != nil {
			if v.Mode != "" {
				mode = v.Mode
			}
			bypass = v.LocalBypass
		}
	}
	c.JSON(http.StatusOK, dto.AuthConfigDTO{
		Mode:        mode,
		LocalBypass: bypass,
	})
}
