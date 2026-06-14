package adapters

import (
	"context"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// NewWebhookReconcileLookup adapts handlers.InstanceRegistry (reload-
// aware map of scan.Instance) to webhookinstall.InstanceLookup. The
// Sonarr client is type-asserted to *sonarr.Client so the reconciler
// can reach notification methods ports.SonarrClient intentionally
// omits. A type-assert miss yields ok=false so a test fixture with a
// fake client degrades to "unknown instance" rather than panicking.
func NewWebhookReconcileLookup(reg handlers.InstanceRegistry) webhookinstall.InstanceLookup {
	return func(name string) (runtime.InstanceSnapshot, webhookinstall.SonarrNotifier, bool) {
		var inst scan.Instance
		var ok bool
		if reg.Load != nil {
			inst, ok = reg.Load()[name]
		}
		if !ok {
			return runtime.InstanceSnapshot{}, nil, false
		}
		concrete, ok := inst.Client.(*sonarr.Client)
		if !ok {
			return runtime.InstanceSnapshot{}, nil, false
		}
		return inst.Config, concrete, true
	}
}

// ReconcilerAdapter widens webhookinstall.Reconciler's (Status, error)
// return to (any, error) so it satisfies application/instance's
// WebhookReconciler interface without that package importing
// application/webhookinstall (which would create a cycle through
// infrastructure/sonarr).
type ReconcilerAdapter struct{ Inner *webhookinstall.Reconciler }

func (a ReconcilerAdapter) Reconcile(ctx context.Context, name string) (any, error) {
	return a.Inner.Reconcile(ctx, name)
}

func (a ReconcilerAdapter) HandleInstanceDeleted(ctx context.Context, name string) {
	a.Inner.HandleInstanceDeleted(ctx, name)
}
