package adapters

import (
	"context"

	"github.com/alexmorbo/seasonfill/application/regrab"
)

// QbitSettingsLoaderFunc is a function-typed shim that satisfies the
// qbitSettingsLoader contract consumed by buildOnAppliedFanout. Defined
// here so the fanout closure can be declared inline at the call site
// without a named struct.
type QbitSettingsLoaderFunc func(ctx context.Context) map[string]regrab.Settings

// Load implements the qbitSettingsLoader interface.
func (f QbitSettingsLoaderFunc) Load(ctx context.Context) map[string]regrab.Settings {
	return f(ctx)
}
