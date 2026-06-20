package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// WebhookChecker satisfies regrab.WebhookChecker. It looks up the
// Sonarr client by instance name (via the reload-bus-fed handlers.
// InstanceRegistry), calls Sonarr's /api/v3/notification list, and
// reports whether any Webhook notification's url field matches the
// canonical `/api/v1/webhook/sonarr/<instance>` path.
//
// The match is prefix-based (per parent §Open-questions §039e
// recommendation) so a stale webhook from an old public URL is still
// recognised — the operator does not need to re-install after a port
// change.
type WebhookChecker struct {
	reg handlers.InstanceRegistry
}

// NewWebhookChecker is the constructor consumed by server.go.
func NewWebhookChecker(reg handlers.InstanceRegistry) *WebhookChecker {
	return &WebhookChecker{reg: reg}
}

// IsInstalled implements regrab.WebhookChecker.
//
// Resolution flow:
//
//  1. Look up the Sonarr client by name from the live registry. Miss
//     → typed error so the settings use case can surface a 502 with a
//     stable message. (In normal operation the registry must contain
//     the instance — the CRUD handler validated the name before
//     reaching the use case.)
//  2. Type-assert the SonarrClient to *sonarr.Client to call
//     ListNotifications (the ports interface intentionally does not
//     surface notification methods — they are config-time only).
//  3. Iterate the notification list; report true if any Webhook
//     notification's url field contains the canonical seasonfill
//     webhook path for this instance.
//
// Transport / type errors propagate as (false, err); pure misses
// return (false, nil) so the use case maps to ErrWebhookNotInstalled.
func (w *WebhookChecker) IsInstalled(ctx context.Context, instanceName domain.InstanceName) (bool, error) {
	var inst scan.Instance
	var ok bool
	if w.reg.Load != nil {
		inst, ok = w.reg.Load()[string(instanceName)]
	}
	if !ok {
		return false, fmt.Errorf("webhook check: %w", ErrUnknownInstance)
	}
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

// ErrUnknownInstance is the sentinel returned when the registry has no
// entry for the supplied name. The settings use case bubbles this as
// ErrWebhookCheckFailed → 502.
var ErrUnknownInstance = errors.New("instance not found in registry")
