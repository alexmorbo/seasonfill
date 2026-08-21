package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// WebhookChecker satisfies regrab.WebhookChecker. It looks up the arr client by
// instance name — Sonarr via the reload-bus-fed catalogrest.InstanceRegistry
// first, falling back to Radarr via the reload-bus-fed
// catalogrest.RadarrConfigLookup (ADR-0023 F3) when the sonarr registry misses
// — calls the arr's /api/v3/notification list, and reports whether any Webhook
// notification's url field matches the canonical
// `/api/v1/webhook/{sonarr,radarr}/<instance>` path.
//
// The match is prefix-based (per parent §Open-questions §039e recommendation)
// so a stale webhook from an old public URL is still recognised — the operator
// does not need to re-install after a port change.
type WebhookChecker struct {
	reg catalogrest.InstanceRegistry
	// radarr is the reload-aware radarr instance lookup (ADR-0023 F3). nil-OK
	// — see WithRadarr godoc. Injected via a builder method (not a
	// constructor param) so NewWebhookChecker's existing one-arg signature,
	// and every existing call site / test, keep compiling unchanged.
	radarr catalogrest.RadarrConfigLookup
}

// NewWebhookChecker is the constructor consumed by the watchdog wirer.
func NewWebhookChecker(reg catalogrest.InstanceRegistry) *WebhookChecker {
	return &WebhookChecker{reg: reg}
}

// WithRadarr injects the reload-aware radarr instance lookup (ADR-0023 F3).
// nil-OK — when unset (or explicitly passed nil), IsInstalled behaves exactly
// as it did before F3: a sonarr-registry miss returns ErrUnknownInstance
// immediately, byte-identical to sonarr-only deployments and to any caller
// that has not been updated to inject the radarr holder. Production is
// wired with *adapters.RadarrInstanceMapHolder (which already satisfies
// catalogrest.RadarrConfigLookup — see internal/catalog/rest/instances.go).
func (w *WebhookChecker) WithRadarr(lookup catalogrest.RadarrConfigLookup) *WebhookChecker {
	w.radarr = lookup
	return w
}

// IsInstalled implements regrab.WebhookChecker.
//
// Resolution flow (ADR-0023 F3):
//
//  1. Look up the instance by name in the live sonarr registry. Hit → dispatch
//     to isInstalledSonarr, UNCHANGED from pre-F3 behavior.
//  2. Sonarr miss AND a radarr lookup is injected (non-nil) → look up the
//     instance by name in the live radarr map. Hit → dispatch to
//     isInstalledRadarr.
//  3. Miss in both (or radarr lookup is nil) → typed ErrUnknownInstance so the
//     settings use case can surface a stable 502. (In normal operation one of
//     the two registries must contain the instance — the CRUD handler
//     validated the name before reaching the use case.)
//
// Transport / type errors propagate as (false, err); pure misses on the
// notification list return (false, nil) so the use case maps to
// ErrWebhookNotInstalled.
func (w *WebhookChecker) IsInstalled(ctx context.Context, instanceName domain.InstanceName) (bool, error) {
	var inst scan.Instance
	var ok bool
	if w.reg.Load != nil {
		inst, ok = w.reg.Load()[string(instanceName)]
	}
	if ok {
		return w.isInstalledSonarr(ctx, instanceName, inst)
	}

	// ADR-0023 F3: sonarr miss → radarr fallback. Same nil-OK fallback shape
	// as QbitDiscoverHandler.Discover (internal/shared/http/handlers/qbit_discover.go).
	if w.radarr != nil {
		if rinst, rok := w.radarr.Load()[string(instanceName)]; rok {
			return w.isInstalledRadarr(ctx, instanceName, rinst)
		}
	}

	return false, fmt.Errorf("webhook check: %w", ErrUnknownInstance)
}

// isInstalledSonarr is the pre-F3 IsInstalled body, unchanged, moved verbatim
// into its own method so IsInstalled can dispatch to it or to
// isInstalledRadarr.
//
//  1. Type-assert the SonarrClient to *sonarr.Client to call
//     ListNotifications (the ports interface intentionally does not surface
//     notification methods — they are config-time only).
//  2. Iterate the notification list; report true if any Webhook
//     notification's url field contains the canonical seasonfill webhook
//     path for this instance.
func (w *WebhookChecker) isInstalledSonarr(ctx context.Context, instanceName domain.InstanceName, inst scan.Instance) (bool, error) {
	if inst.Client == nil {
		return false, fmt.Errorf("webhook check: instance %q has nil client", instanceName)
	}
	concrete, ok := inst.Client.(*sonarr.Client)
	if !ok {
		return false, fmt.Errorf("webhook check: instance %q client is not *sonarr.Client", instanceName)
	}
	notifications, err := concrete.ListNotifications(ctx)
	if err != nil {
		return false, fmt.Errorf("webhook check: list notifications for %q: %w", instanceName, err)
	}

	canonical := strings.ToLower("/api/v1/webhook/sonarr/" + string(instanceName))
	for _, n := range notifications {
		if !strings.EqualFold(n.Implementation, "Webhook") {
			continue
		}
		if !n.OnGrab {
			// We only enforce that OnGrab is enabled — that is the
			// trigger the regrab loop actually depends on. OnImport
			// and OnImportFailure ride on the same notification but
			// are not required for the gate.
			continue
		}
		for _, f := range n.Fields {
			if f.Name != "url" {
				continue
			}
			s, ok := f.Value.(string)
			if !ok {
				continue
			}
			if strings.Contains(strings.ToLower(s), canonical) {
				return true, nil
			}
		}
	}
	return false, nil
}

// isInstalledRadarr is the radarr twin of isInstalledSonarr (ADR-0023 F3).
// Identical OnGrab+Webhook+url-contains match, against the radarr canonical
// path instead of the sonarr one. Type-asserts to *radarr.Client for the same
// reason isInstalledSonarr asserts to *sonarr.Client: ports.RadarrClient
// deliberately does not surface ListNotifications (config-time-only surface).
func (w *WebhookChecker) isInstalledRadarr(ctx context.Context, instanceName domain.InstanceName, inst scan.RadarrInstance) (bool, error) {
	if inst.Client == nil {
		return false, fmt.Errorf("webhook check: instance %q has nil client", instanceName)
	}
	concrete, ok := inst.Client.(*radarr.Client)
	if !ok {
		return false, fmt.Errorf("webhook check: instance %q client is not *radarr.Client", instanceName)
	}
	notifications, err := concrete.ListNotifications(ctx)
	if err != nil {
		return false, fmt.Errorf("webhook check: list notifications for %q: %w", instanceName, err)
	}

	canonical := strings.ToLower("/api/v1/webhook/radarr/" + string(instanceName))
	for _, n := range notifications {
		if !strings.EqualFold(n.Implementation, "Webhook") {
			continue
		}
		if !n.OnGrab {
			continue
		}
		for _, f := range n.Fields {
			if f.Name != "url" {
				continue
			}
			s, ok := f.Value.(string)
			if !ok {
				continue
			}
			if strings.Contains(strings.ToLower(s), canonical) {
				return true, nil
			}
		}
	}
	return false, nil
}

// ErrUnknownInstance is the sentinel returned when neither registry has an
// entry for the supplied name. The settings use case bubbles this as
// ErrWebhookCheckFailed → 502.
var ErrUnknownInstance = errors.New("instance not found in registry")
