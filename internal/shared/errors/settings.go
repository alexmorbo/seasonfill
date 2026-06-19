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

// AppSettingsNotFoundError signals the singleton app_settings row is
// missing. Maps to HTTP 404.
type AppSettingsNotFoundError struct{}

func (e *AppSettingsNotFoundError) Error() string { return "app settings not found" }

func (e *AppSettingsNotFoundError) Code() string { return "app_settings_not_found" }

func (e *AppSettingsNotFoundError) Retriable() bool { return false }

// QbitSettingsNotFoundError signals a missing qbit_settings row for the
// given instance. Maps to HTTP 404.
type QbitSettingsNotFoundError struct {
	InstanceName domain.InstanceName
}

func (e *QbitSettingsNotFoundError) Error() string {
	return fmt.Sprintf("qbit settings for instance %q not found", e.InstanceName)
}

func (e *QbitSettingsNotFoundError) Code() string { return "qbit_settings_not_found" }

func (e *QbitSettingsNotFoundError) Retriable() bool { return false }
