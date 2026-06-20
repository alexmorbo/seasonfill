package adapters

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/webhookinstall"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// NewWebhookReconcileLookup adapts catalogrest.InstanceRegistry (reload-
// aware map of scan.Instance) to webhookinstall.InstanceLookup. The
// Sonarr client is type-asserted to *sonarr.Client so the reconciler
// can reach notification methods ports.SonarrClient intentionally
// omits. A type-assert miss yields ok=false so a test fixture with a
// fake client degrades to "unknown instance" rather than panicking.
func NewWebhookReconcileLookup(reg catalogrest.InstanceRegistry) webhookinstall.InstanceLookup {
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

func (a ReconcilerAdapter) Reconcile(ctx context.Context, name domain.InstanceName) (any, error) {
	return a.Inner.Reconcile(ctx, string(name))
}

func (a ReconcilerAdapter) HandleInstanceDeleted(ctx context.Context, name domain.InstanceName) {
	a.Inner.HandleInstanceDeleted(ctx, string(name))
}
