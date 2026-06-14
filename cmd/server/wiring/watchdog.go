package wiring

import (
	"log/slog"

	"github.com/alexmorbo/seasonfill/infrastructure/watchdog"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
)

// WatchdogBundle holds the boot-time health monitor + state watchdog
// for per-instance Sonarr availability. Constructed once by
// BuildWatchdog; neither field is mutated after boot.
//
// Field-level invariants:
//
//   - Checker drives the periodic healthcheck loop (see server.go
//     lifecycle.Go("healthcheck")) and exposes the registry that the
//     scan UC (WithHealthRegistry) and the Watchdog rechecker share.
//
//   - Watchdog rechecks Unavailable* instances at per-state cadences
//     (D-2.3). It reads from Checker.Registry() and dispatches
//     rechecks back through Checker.
type WatchdogBundle struct {
	Checker  *healthcheck.Checker
	Watchdog *watchdog.Watchdog
}

// BuildWatchdog wires the healthcheck Checker and the state Watchdog
// against the persistence DB handle, the boot Sonarr client set, and
// the per-instance HealthCheck config map. No error path — every step
// is in-memory construction. The signature returns error to leave
// room for future seed-or-validate logic without a downstream
// signature churn.
func BuildWatchdog(persistence *PersistenceBundle, sonarr *SonarrBundle, log *slog.Logger) (*WatchdogBundle, error) {
	checker := healthcheck.New(persistence.DB, sonarr.SonarrClients)
	wd := watchdog.New(checker.Registry(), checker, log, sonarr.CfgByName)

	return &WatchdogBundle{
		Checker:  checker,
		Watchdog: wd,
	}, nil
}
