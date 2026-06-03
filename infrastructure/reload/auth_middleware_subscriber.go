package reload

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"reflect"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

type AuthMiddlewareSubscriber struct {
	ptr    *middleware.AuthRuntimePointer
	engine *gin.Engine
	logger *slog.Logger
}

func NewAuthMiddlewareSubscriber(ptr *middleware.AuthRuntimePointer, engine *gin.Engine, logger *slog.Logger) *AuthMiddlewareSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthMiddlewareSubscriber{ptr: ptr, engine: engine, logger: logger}
}

func (s *AuthMiddlewareSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "authMiddleware", s.logger, s.apply, ready)
}

func (s *AuthMiddlewareSubscriber) apply(ctx context.Context, snap runtime.Snapshot) error {
	mode := snap.Auth.Mode
	if mode == "" {
		mode = runtime.AuthModeForms
	}
	parsed := s.parseLocalNetworks(ctx, snap.Auth.LocalNetworks)
	want := middleware.AuthRuntime{
		SessionTTL:     snap.Auth.SessionTTL,
		TrustedProxies: append([]string(nil), snap.Auth.TrustedProxies...),
		SecureCookie:   snap.Auth.SecureCookie,
		Mode:           mode,
		LocalBypass:    snap.Auth.LocalBypass,
		LocalNetworks:  parsed,
		SessionEpoch:   snap.Auth.SessionEpoch,
		OIDC: middleware.OIDCRuntime{
			Issuer:        snap.Auth.OIDC.Issuer,
			ClientID:      snap.Auth.OIDC.ClientID,
			RedirectURL:   snap.Auth.OIDC.RedirectURL,
			Scopes:        append([]string(nil), snap.Auth.OIDC.Scopes...),
			UsernameClaim: snap.Auth.OIDC.UsernameClaim,
			AllowedGroups: append([]string(nil), snap.Auth.OIDC.AllowedGroups...),
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

// parseLocalNetworks pre-parses CIDR strings once per reload. Bad
// entries are logged + skipped (NOT fatal) — a single malformed CIDR
// must not poison the whole apply. The bypass hot path (036c) treats a
// nil slice as "bypass disabled", which is the safe fail-closed
// behaviour.
func (s *AuthMiddlewareSubscriber) parseLocalNetworks(ctx context.Context, raw []string) []*net.IPNet {
	if len(raw) == 0 {
		return nil
	}
	out := make([]*net.IPNet, 0, len(raw))
	for _, entry := range raw {
		_, ipnet, err := net.ParseCIDR(entry)
		if err != nil {
			s.logger.WarnContext(ctx, "authMiddleware.local_network_invalid",
				slog.String("entry", entry),
				slog.String("error", err.Error()))
			continue
		}
		out = append(out, ipnet)
	}
	return out
}

func authRuntimeEqual(a, b *middleware.AuthRuntime) bool {
	if a.SessionTTL != b.SessionTTL ||
		a.SecureCookie != b.SecureCookie ||
		a.Mode != b.Mode ||
		a.LocalBypass != b.LocalBypass ||
		a.SessionEpoch != b.SessionEpoch {
		return false
	}
	if !reflect.DeepEqual(a.TrustedProxies, b.TrustedProxies) {
		return false
	}
	if len(a.LocalNetworks) != len(b.LocalNetworks) {
		return false
	}
	for i := range a.LocalNetworks {
		if a.LocalNetworks[i].String() != b.LocalNetworks[i].String() {
			return false
		}
	}
	if a.OIDC.Issuer != b.OIDC.Issuer ||
		a.OIDC.ClientID != b.OIDC.ClientID ||
		a.OIDC.RedirectURL != b.OIDC.RedirectURL ||
		a.OIDC.UsernameClaim != b.OIDC.UsernameClaim {
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
