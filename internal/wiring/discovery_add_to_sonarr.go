package wiring

import (
	"context"
	"errors"
	"log/slog"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	adminpersistence "github.com/alexmorbo/seasonfill/internal/admin/persistence"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// addRegistryLookup adapts catalogrest.InstanceRegistry into
// discoapp.AddInstanceLookup. The use case only needs (client, ok) —
// the integer id is dropped because the per-instance Sonarr client
// already carries its name and tags are written through the client.
type addRegistryLookup struct {
	reg catalogrest.InstanceRegistry
}

func (r addRegistryLookup) Lookup(name string) (ports.SonarrClient, bool) {
	inst, ok := r.reg.Snapshot()[name]
	if !ok {
		return nil, false
	}
	return inst.Client, true
}

// meUserResolver adapts *authapp.MeUseCase into
// discoapp.CurrentUserResolver. ports.ErrNotFound → nil user / nil
// error so the use case treats the request as bypass instead of
// surfacing a 404 for an unknown context username.
type meUserResolver struct {
	me *authapp.MeUseCase
}

func (r meUserResolver) GetCurrent(ctx context.Context, username string) (*admin.User, error) {
	u, err := r.me.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// BuildDiscoveryAddToSonarr wires the N-4c TagResolver + use case +
// handler. The MeUseCase is reconstructed off AdminRepo because the
// AuthBundle does not expose it as a field (MeHandler holds it
// internally); this keeps the existing AuthBundle shape unchanged.
//
// log MUST already carry the parent context tag — callers pass the
// process logger and this function attaches "discovery" via
// sharedports.DomainLogger.
func BuildDiscoveryAddToSonarr(
	auth *AuthBundle,
	sonarrBundle *SonarrBundle,
	persistence *PersistenceBundle,
	log *slog.Logger,
) *discoveryrest.AddToSonarrHandler {
	domainLog := sharedports.DomainLogger(log, "discovery")
	tagRepo := adminpersistence.NewUserInstanceTagRepository(persistence.DB)
	resolver := discoapp.NewTagResolver(tagRepo, domainLog)
	users := meUserResolver{me: authapp.NewMeUseCase(auth.AdminRepo)}
	lookup := addRegistryLookup{reg: sonarrBundle.InstanceReg}
	uc := discoapp.NewAddToSonarrUseCase(lookup, users, resolver, domainLog)
	return discoveryrest.NewAddToSonarrHandler(uc, domainLog)
}
