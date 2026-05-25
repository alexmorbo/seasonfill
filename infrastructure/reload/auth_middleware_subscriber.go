package reload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

func (s *AuthMiddlewareSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	want := middleware.AuthRuntime{
		SessionTTL:     snap.Auth.SessionTTL,
		TrustedProxies: append([]string(nil), snap.Auth.TrustedProxies...),
		SecureCookie:   snap.Auth.SecureCookie,
	}
	prev := s.ptr.Load()
	// Skip store + engine update when nothing changed.
	if prev != nil && prev.SessionTTL == want.SessionTTL &&
		prev.SecureCookie == want.SecureCookie &&
		reflect.DeepEqual(prev.TrustedProxies, want.TrustedProxies) {
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

var errNilEngine = errors.New("authMiddleware: engine is nil")
