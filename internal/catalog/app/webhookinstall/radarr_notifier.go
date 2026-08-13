package webhookinstall

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
)

// RadarrNotifier is the narrow Radarr-mutation surface the reconciler needs.
// Additive twin of SonarrNotifier (M-FIX-4b) — defined here, NOT on
// ports.RadarrClient, for the same reason: these methods are config-time and
// only the reconciler consumes them, so keeping them off the shared port avoids
// forcing every existing RadarrClient mock to grow notification stubs.
type RadarrNotifier interface {
	ListNotifications(ctx context.Context) ([]radarr.Notification, error)
	CreateNotification(ctx context.Context, p radarr.NotificationPayload) (radarr.Notification, error)
	UpdateNotification(ctx context.Context, existing radarr.Notification, p radarr.NotificationPayload) (radarr.Notification, error)
	// TestNotification asks Radarr to exercise the webhook end-to-end
	// (POST /api/v3/notification/test). The reconciler gates Installed:true on
	// its success so a webhook that installs but cannot deliver surfaces as
	// LastError rather than a silent green badge.
	TestNotification(ctx context.Context, p radarr.NotificationPayload) error
	DeleteNotification(ctx context.Context, id int) error
}

// RadarrLookup resolves a radarr instance name to its current snapshot plus a
// RadarrNotifier. ok=false → caller falls through (treated as unknown instance).
// Optional on the Reconciler: nil radarrLookup disables the radarr branch
// entirely (sonarr-only deployments / tests that never set it).
type RadarrLookup func(name string) (snap runtime.InstanceSnapshot, notifier RadarrNotifier, ok bool)
