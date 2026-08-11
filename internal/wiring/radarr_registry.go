package wiring

import (
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	moviecollection "github.com/alexmorbo/seasonfill/internal/catalog/app/moviecollection"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	radarrclient "github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// radarrAddLookup adapts the radarr holder into discoapp.AddRadarrInstanceLookup.
// Returns the reload-aware per-instance ports.RadarrClient (LookupMovie/AddMovie).
type radarrAddLookup struct {
	holder *adapters.RadarrInstanceMapHolder
}

func (l radarrAddLookup) Lookup(name string) (ports.RadarrClient, bool) {
	inst, ok := l.holder.Load()[name]
	if !ok || inst.Client == nil {
		return nil, false
	}
	return inst.Client, true
}

var _ discoapp.AddRadarrInstanceLookup = radarrAddLookup{}

// radarrCollectionLookup adapts the radarr holder into
// moviecollection.RadarrCollectionInstanceLookup. The collection methods
// (GetCollections/PutCollection) live on the concrete *radarr.Client, so this
// type-asserts (same pattern as the sonarrFor closures asserting *sonarr.Client).
type radarrCollectionLookup struct {
	holder *adapters.RadarrInstanceMapHolder
}

func (l radarrCollectionLookup) Lookup(name string) (moviecollection.RadarrCollectionClient, bool) {
	inst, ok := l.holder.Load()[name]
	if !ok || inst.Client == nil {
		return nil, false
	}
	concrete, ok := inst.Client.(*radarrclient.Client)
	if !ok {
		return nil, false
	}
	return concrete, true
}

var _ moviecollection.RadarrCollectionInstanceLookup = radarrCollectionLookup{}
