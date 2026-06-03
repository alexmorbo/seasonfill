package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

const oidcLoginPath = "/api/v1/auth/oidc/start"

type AuthConfigHandler struct {
	runtime *middleware.AuthRuntimePointer
}

func NewAuthConfigHandler(ptr *middleware.AuthRuntimePointer) *AuthConfigHandler {
	return &AuthConfigHandler{runtime: ptr}
}

// Get returns {mode, local_bypass, login_url?}. Public — never gated.
// login_url is set only when mode=oidc (the SPA reads it to render the
// "Login with SSO" button); other modes get the existing 2-field shape.
//
// @Summary     Public auth-mode bootstrap
// @Tags        auth
// @Produce     json
// @Success     200  {object}  dto.AuthConfigDTO
// @Router      /auth/config [get]
func (h *AuthConfigHandler) Get(c *gin.Context) {
	mode := runtime.AuthModeForms
	bypass := false
	loginURL := ""
	if h.runtime != nil {
		if v := h.runtime.Load(); v != nil {
			if v.Mode != "" {
				mode = v.Mode
			}
			bypass = v.LocalBypass
			if mode == runtime.AuthModeOIDC {
				loginURL = oidcLoginPath
			}
		}
	}
	c.JSON(http.StatusOK, dto.AuthConfigDTO{
		Mode:        mode,
		LocalBypass: bypass,
		LoginURL:    loginURL,
	})
}
