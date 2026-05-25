// Package reload — shared types used across multiple subscribers.
package reload

import "github.com/alexmorbo/seasonfill/application/ports"

// ClientLister returns the current live sonarr client list. Implemented
// by SonarrClientsSubscriber.View().All(). Used by HealthRegistrySubscriber.
type ClientLister func() []ports.SonarrClient

// ClientForName returns the live sonarr client for `name`. Implemented
// by SonarrClientsSubscriber.View().ByName(). Used by ScanInstancesSubscriber.
type ClientForName func(name string) (ports.SonarrClient, bool)
