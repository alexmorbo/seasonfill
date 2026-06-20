//go:build !integration

package main

import (
	"sync/atomic"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/admin/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/instance"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/reload"
)

// notifyTestContext is a no-op in non-integration builds. The integration
// build provides the real implementation in testcontext_hook.go.
func notifyTestContext(
	_ *runtime.Bus,
	_ *reload.SchedulerSubscriber,
	_ *reload.SonarrClientsSubscriber,
	_ *middleware.AuthRuntimePointer,
	_ *atomic.Pointer[ratelimit.Limiter],
	_ func() map[string]scan.Instance,
	_ func() []instance.Snapshot,
) {
}
