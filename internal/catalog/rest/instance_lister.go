package rest

import "context"

// InstanceLister returns every configured Sonarr instance name. Lives
// here (rather than next to a single handler) because two unrelated
// handlers consume the same shape: WebhooksAggregateHandler (this
// package) and the watchdog rollups handler in internal/watchdog/rest.
// Story 434 A-1-8 lifted the type out of watchdog_rollups.go when that
// file moved into the watchdog vertical slice; defining the interface
// in a shared spot avoids forcing the webhooks handler to import the
// watchdog REST package solely for a type reference.
type InstanceLister interface {
	ListNames(ctx context.Context) ([]string, error)
}
