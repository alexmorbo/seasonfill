package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// RuntimeConfigNotFoundError signals the singleton runtime_config row
// is missing. Triggered on cold-boot or after a deliberate truncate;
// the application layer treats it as "use defaults". Maps to HTTP 404.
type RuntimeConfigNotFoundError struct{}

func (e *RuntimeConfigNotFoundError) Error() string { return "runtime config not found" }

func (e *RuntimeConfigNotFoundError) Code() string { return "runtime_config_not_found" }

func (e *RuntimeConfigNotFoundError) Retriable() bool { return false }

// QbitSettingsNotFoundError signals a missing qbit_settings row for the
// given instance. Maps to HTTP 404. The repository looks rows up by
// the typed instance name (PK of qbit_settings) — D-1 moved
// sonarr_instance to a TEXT name PK so the surrogate uint went away
// (D-6 / story 467c).
type QbitSettingsNotFoundError struct {
	InstanceName domain.InstanceName
}

func (e *QbitSettingsNotFoundError) Error() string {
	return fmt.Sprintf("qbit settings for instance %q not found", e.InstanceName)
}

func (e *QbitSettingsNotFoundError) Code() string { return "qbit_settings_not_found" }

func (e *QbitSettingsNotFoundError) Retriable() bool { return false }
