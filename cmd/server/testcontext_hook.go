//go:build integration

package main

import (
	"sync/atomic"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// TestContext carries the live per-subscriber pointers for integration assertions.
// Only present in integration builds; zero production footprint.
type TestContext struct {
	Bus             *runtime.Bus
	SubSched        *reload.SchedulerSubscriber
	ClientsView     func() *reload.ClientsView
	AuthRuntimePtr  *middleware.AuthRuntimePointer
	GlobalLimPtr    *atomic.Pointer[ratelimit.Limiter]
	HolderSnapshot  func() map[string]scan.Instance
	CheckerSnapshot func() []instance.Snapshot
}

// testContextHook is set by bootForTestWithContext before runForTest starts.
// runWithContext calls notifyTestContext (if non-nil) after all subscribers
// are ready. In non-integration builds this symbol does not exist.
var testContextHook func(*TestContext)

// notifyTestContext populates and fires testContextHook if it is set.
// Called from runWithContext after startSubscribers + boot Publish.
func notifyTestContext(
	bus *runtime.Bus,
	subSched *reload.SchedulerSubscriber,
	subClients *reload.SonarrClientsSubscriber,
	authRuntimePtr *middleware.AuthRuntimePointer,
	globalLimiterPtr *atomic.Pointer[ratelimit.Limiter],
	holderLoad func() map[string]scan.Instance,
	checkerSnapshot func() []instance.Snapshot,
) {
	if testContextHook == nil {
		return
	}
	testContextHook(&TestContext{
		Bus:             bus,
		SubSched:        subSched,
		ClientsView:     subClients.View,
		AuthRuntimePtr:  authRuntimePtr,
		GlobalLimPtr:    globalLimiterPtr,
		HolderSnapshot:  holderLoad,
		CheckerSnapshot: checkerSnapshot,
	})
}
