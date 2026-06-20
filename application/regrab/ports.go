// Package regrab is the Phase 10 Watchdog application layer. Per
// parent story 039 D-T1 the Go package is named `regrab` (not
// `watchdog`) to avoid collision with `infrastructure/watchdog/`
// (D24 instance-health recheck loop).
package regrab

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/domain/decision"
	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

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
	IsInstalled(ctx context.Context, instanceName domain.InstanceName) (bool, error)
}

// nullWebhookChecker is the bootstrap-time default. Used when
// the application/regrab.UseCase is constructed without
// WithWebhookChecker (e.g. unit tests that don't exercise the
// gate). Always reports installed=true so the gate is open.
type nullWebhookChecker struct{}

func (nullWebhookChecker) IsInstalled(_ context.Context, _ domain.InstanceName) (bool, error) {
	return true, nil
}

// errCipherRequired is returned by NewSettingsFromRecord (types.go) when
// the settings row carries non-empty PasswordEncrypted but the caller
// passed a nil cipher. Exported as a sentinel so the 039f-2 use case can
// surface a typed metric label without string-comparing the error.
var errCipherRequired = errors.New("regrab: cipher required to decrypt password")

// EvaluateExecutor is the regrab use case's view of the evaluator. The
// real *evaluate.UseCase satisfies this interface implicitly via Execute.
// Defining it here lets the regrab use case unit tests stub the
// evaluator without spinning up real Sonarr stubs.
type EvaluateExecutor interface {
	Execute(ctx context.Context, in evaluate.Input) (decision.Decision, error)
}

// GrabExecutor is the regrab use case's view of the grab use case. The
// real *grab.UseCase satisfies this interface implicitly via Execute.
// The use case calls this only when the evaluator returned
// OutcomeGrab — i.e. the candidate is already chosen and ready for
// ForceGrab.
type GrabExecutor interface {
	Execute(ctx context.Context, in grab.Input) grab.Output
}

// Metrics is the regrab use case's metric emitter. The reload-bus
// subscriber (039g) wires the production implementation (which
// translates calls to observability.* metrics with the frozen label
// keys from parent D-T5). Unit tests use the package-private
// nullMetrics default — emits nothing, never panics.
//
// All three method signatures share `(instance domain.InstanceName)` as
// the first arg because every Watchdog metric has `instance` as its
// primary label.
type Metrics interface {
	// IncPollResult bumps seasonfill_watchdog_poll_total{instance, result}.
	IncPollResult(instance domain.InstanceName, result string)

	// IncUnregistered bumps seasonfill_watchdog_unregistered_detected_total{instance, tracker}.
	IncUnregistered(instance domain.InstanceName, tracker string)

	// IncRegrabResult bumps seasonfill_watchdog_regrab_triggered_total{instance, result}.
	IncRegrabResult(instance domain.InstanceName, result string)

	// SetBlacklistSize replaces the gauge seasonfill_watchdog_blacklist_size{instance}.
	SetBlacklistSize(instance domain.InstanceName, size int)

	// SetQbitUnreachableStreak replaces the gauge seasonfill_watchdog_qbit_unreachable_streak{instance}.
	SetQbitUnreachableStreak(instance domain.InstanceName, streak int)
}

// nullMetrics is the bootstrap-time default. The regrab use case
// constructor swaps this for a production Metrics impl via
// WithMetrics(); unit tests rely on this default so they never need
// to thread mocks through.
type nullMetrics struct{}

func (nullMetrics) IncPollResult(domain.InstanceName, string)         {}
func (nullMetrics) IncUnregistered(domain.InstanceName, string)       {}
func (nullMetrics) IncRegrabResult(domain.InstanceName, string)       {}
func (nullMetrics) SetBlacklistSize(domain.InstanceName, int)         {}
func (nullMetrics) SetQbitUnreachableStreak(domain.InstanceName, int) {}

// Compile-time blank assignments — keep the deferred-import compiler
// happy so a future refactor that drops one of the import sites here
// flags clearly.
var (
	_ = (*time.Duration)(nil)
	_ = uuid.New
	_ = domaingrab.Record{}
)
