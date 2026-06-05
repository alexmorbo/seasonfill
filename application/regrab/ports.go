// Package regrab is the Phase 10 Watchdog application layer. Per
// parent story 039 D-T1 the Go package is named `regrab` (not
// `watchdog`) to avoid collision with `infrastructure/watchdog/`
// (D24 instance-health recheck loop).
package regrab

import "context"

// WebhookChecker is the boundary the Watchdog settings use case
// uses to enforce parent invariant C-3 (webhook-required gate).
// 039d defines the interface; 039e implements it (calls Sonarr
// `/api/v3/notification` and looks for a matching URL prefix);
// 039g wires the concrete implementation into the settings use
// case at cmd/server boot.
//
// Until 039g lands, the settings use case accepts a
// nullWebhookChecker that always returns (true, nil) so the
// settings CRUD is fully functional in isolation and the
// integration test in 039g flips it for the gate behaviour.
//
// IsInstalled MUST return:
//   - (true,  nil) when an OnGrab webhook pointing at the
//     canonical seasonfill webhook URL exists.
//   - (false, nil) when no matching webhook is present.
//   - (_,     err) only on transport / Sonarr-side failures —
//     the use case maps this to 502 and skips persistence.
type WebhookChecker interface {
	IsInstalled(ctx context.Context, instanceName string) (bool, error)
}

// nullWebhookChecker is the bootstrap-time default. Used when
// the application/regrab.UseCase is constructed without
// WithWebhookChecker (e.g. unit tests that don't exercise the
// gate). Always reports installed=true so the gate is open.
type nullWebhookChecker struct{}

func (nullWebhookChecker) IsInstalled(_ context.Context, _ string) (bool, error) {
	return true, nil
}
