package webhookinstall

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

// SonarrNotifier is the narrow Sonarr-mutation surface the reconciler
// needs. Defined here (not in application/ports) because the methods
// are config-time and only the reconciler consumes them — keeping
// them off ports.SonarrClient avoids forcing every existing mock to
// grow notification stubs.
type SonarrNotifier interface {
	ListNotifications(ctx context.Context) ([]sonarr.Notification, error)
	CreateNotification(ctx context.Context, p sonarr.NotificationPayload) (sonarr.Notification, error)
	UpdateNotification(ctx context.Context, existing sonarr.Notification, p sonarr.NotificationPayload) (sonarr.Notification, error)
	// TestNotification asks Sonarr to exercise the webhook end-to-end
	// (POST /api/v3/notification/test). The reconciler gates Installed:true
	// on its success so a webhook that installs but cannot deliver surfaces
	// as LastError rather than a silent green badge.
	TestNotification(ctx context.Context, p sonarr.NotificationPayload) error
	DeleteNotification(ctx context.Context, id int) error
}

// InstanceLookup resolves an instance name to its current snapshot
// plus a SonarrNotifier. ok=false → caller treats as unknown instance.
type InstanceLookup func(name string) (snap runtime.InstanceSnapshot, notifier SonarrNotifier, ok bool)

// PublicURLFunc returns the seasonfill-side public base URL the
// reconciler should install when the instance has no
// WebhookURLOverride. Empty → "cannot determine".
type PublicURLFunc func(ctx context.Context) string
