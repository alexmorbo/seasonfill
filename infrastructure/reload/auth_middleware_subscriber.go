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

// AuthMiddlewareSubscriber pushes new {SessionTTL, TrustedProxies}
// values onto the shared atomic and onto the live gin engine. The
// engine is the same one served by the http.Server; SetTrustedProxies
// is thread-safe per gin's contract.
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

func (s *AuthMiddlewareSubscriber) Run(ctx context.Context, bus *runtime.Bus) {
	runLoop(ctx, bus, "authMiddleware", s.logger, s.apply)
}

func (s *AuthMiddlewareSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	want := middleware.AuthRuntime{
		SessionTTL:     snap.Auth.SessionTTL,
		TrustedProxies: append([]string(nil), snap.Auth.TrustedProxies...),
	}
	// Always store the new pointer first so AuthHandler.Login picks
	// up the new TTL on the very next request.
	prev := s.ptr.Load()
	s.ptr.Store(&want)

	if s.engine == nil {
		return nil
	}
	// Only call SetTrustedProxies if the list actually changed.
	if prev != nil && reflect.DeepEqual(prev.TrustedProxies, want.TrustedProxies) {
		return nil
	}
	if err := s.engine.SetTrustedProxies(want.TrustedProxies); err != nil {
		// Roll back the engine to nil (RemoteAddr-only) per
		// HIGH-S2; the atomic stays on the new value so SessionTTL
		// changes still take effect.
		_ = s.engine.SetTrustedProxies(nil)
		return fmt.Errorf("set trusted proxies: %w", err)
	}
	return nil
}

// errNilEngine is exposed for tests that exercise the nil-engine
// path explicitly (cmd/server always passes a non-nil engine; the
// guard exists to keep early-init mistakes obvious).
var errNilEngine = errors.New("authMiddleware: engine is nil")
