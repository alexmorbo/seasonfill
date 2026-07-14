package rest

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

const oidcLoginPath = "/api/v1/auth/oidc/start"

type AuthConfigHandler struct {
	runtime *middleware.AuthRuntimePointer
}

func NewAuthConfigHandler(ptr *middleware.AuthRuntimePointer) *AuthConfigHandler {
	return &AuthConfigHandler{runtime: ptr}
}

// Get returns {oidc_ready, login_url?}. Public — never gated.
// login_url is set whenever oidc_ready=true (the SPA reads it to render the
// "Login with SSO" button); otherwise only oidc_ready is emitted.
//
// @Summary     Public auth-config bootstrap
// @Tags        auth
// @Produce     json
// @Success     200  {object}  dto.AuthConfigDTO
// @Router      /auth/config [get]
func (h *AuthConfigHandler) Get(c *gin.Context) {
	oidcReady := false
	loginURL := ""
	if h.runtime != nil {
		if v := h.runtime.Load(); v != nil {
			oidcReady = v.OIDC.IsReady()
			if oidcReady {
				loginURL = oidcLoginPath
			}
		}
	}
	c.JSON(http.StatusOK, dto.AuthConfigDTO{
		OIDCReady: oidcReady, LoginURL: loginURL,
	})
}
