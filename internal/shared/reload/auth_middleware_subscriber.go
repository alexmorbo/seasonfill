package reload

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type AuthMiddlewareSubscriber struct {
	ptr             *middleware.AuthRuntimePointer
	engine          *gin.Engine
	logger          *slog.Logger
	runtimeRepo     ports.RuntimeConfigRepository
	clientSecretEnv string
}

func NewAuthMiddlewareSubscriber(
	ptr *middleware.AuthRuntimePointer,
	engine *gin.Engine,
	logger *slog.Logger,
	runtimeRepo ports.RuntimeConfigRepository,
	clientSecretEnv string,
) *AuthMiddlewareSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthMiddlewareSubscriber{
		ptr: ptr, engine: engine, logger: logger,
		runtimeRepo: runtimeRepo, clientSecretEnv: clientSecretEnv,
	}
}

func (s *AuthMiddlewareSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "authMiddleware", s.logger, s.apply, ready)
}

func (s *AuthMiddlewareSubscriber) apply(ctx context.Context, snap runtime.Snapshot) error {
	mode := snap.Auth.Mode
	if mode == "" {
		mode = runtime.AuthModeForms
	}
	resolvedSecret := s.resolveClientSecret(ctx)
	want := middleware.AuthRuntime{
		SessionTTL:     snap.Auth.SessionTTL,
		TrustedProxies: append([]string(nil), snap.Auth.TrustedProxies...),
		SecureCookie:   snap.Auth.SecureCookie,
		Mode:           mode,
		SessionEpoch:   snap.Auth.SessionEpoch,
		OIDC: middleware.OIDCRuntime{
			Issuer:        snap.Auth.OIDC.Issuer,
			ClientID:      snap.Auth.OIDC.ClientID,
			ClientSecret:  resolvedSecret,
			RedirectURL:   snap.Auth.OIDC.RedirectURL,
			Scopes:        append([]string(nil), snap.Auth.OIDC.Scopes...),
			UsernameClaim: snap.Auth.OIDC.UsernameClaim,
			AllowedGroups: append([]string(nil), snap.Auth.OIDC.AllowedGroups...),
			GroupsClaim:   snap.Auth.OIDC.GroupsClaim,
		},
	}
	prev := s.ptr.Load()
	if prev != nil && authRuntimeEqual(prev, &want) {
		return nil
	}
	s.ptr.Store(&want)
	if s.engine == nil {
		return nil
	}
	if prev != nil && reflect.DeepEqual(prev.TrustedProxies, want.TrustedProxies) {
		return nil
	}
	if err := s.engine.SetTrustedProxies(want.TrustedProxies); err != nil {
		_ = s.engine.SetTrustedProxies(nil)
		return fmt.Errorf("set trusted proxies: %w", err)
	}
	return nil
}

// resolveClientSecret returns env > DB-decrypted; "" if neither.
func (s *AuthMiddlewareSubscriber) resolveClientSecret(ctx context.Context) string {
	if s.clientSecretEnv != "" {
		return s.clientSecretEnv
	}
	if s.runtimeRepo == nil {
		return ""
	}
	secret, err := s.runtimeRepo.DecryptOIDCSecret(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "authMiddleware.oidc_decrypt_failed",
			slog.String("error", err.Error()))
		return ""
	}
	return secret
}

func authRuntimeEqual(a, b *middleware.AuthRuntime) bool {
	if a.SessionTTL != b.SessionTTL ||
		a.SecureCookie != b.SecureCookie ||
		a.Mode != b.Mode ||
		a.SessionEpoch != b.SessionEpoch {
		return false
	}
	if !reflect.DeepEqual(a.TrustedProxies, b.TrustedProxies) {
		return false
	}
	if a.OIDC.Issuer != b.OIDC.Issuer ||
		a.OIDC.ClientID != b.OIDC.ClientID ||
		a.OIDC.ClientSecret != b.OIDC.ClientSecret ||
		a.OIDC.RedirectURL != b.OIDC.RedirectURL ||
		a.OIDC.UsernameClaim != b.OIDC.UsernameClaim ||
		a.OIDC.GroupsClaim != b.OIDC.GroupsClaim {
		return false
	}
	if !reflect.DeepEqual(a.OIDC.Scopes, b.OIDC.Scopes) {
		return false
	}
	if !reflect.DeepEqual(a.OIDC.AllowedGroups, b.OIDC.AllowedGroups) {
		return false
	}
	return true
}
