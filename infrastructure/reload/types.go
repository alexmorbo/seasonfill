// Package reload — shared types used across multiple subscribers.
package reload

import "github.com/alexmorbo/seasonfill/application/ports"

// ClientLister returns the current live sonarr client list. Implemented
// by SonarrClientsSubscriber.View().All(). Used by HealthRegistrySubscriber.
type ClientLister func() []ports.SonarrClient
