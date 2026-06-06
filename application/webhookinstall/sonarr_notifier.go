package webhookinstall

import (
	"context"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/runtime"
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
	DeleteNotification(ctx context.Context, id int) error
}

// InstanceLookup resolves an instance name to its current snapshot
// plus a SonarrNotifier. ok=false → caller treats as unknown instance.
type InstanceLookup func(name string) (snap runtime.InstanceSnapshot, notifier SonarrNotifier, ok bool)

// PublicURLFunc returns the seasonfill-side public base URL the
// reconciler should install when the instance has no
// WebhookURLOverride. Empty → "cannot determine".
type PublicURLFunc func(ctx context.Context) string
