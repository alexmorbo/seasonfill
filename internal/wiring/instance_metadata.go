package wiring

import (
	"log/slog"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admininfra "github.com/alexmorbo/seasonfill/internal/admin/infrastructure"
	adminrest "github.com/alexmorbo/seasonfill/internal/admin/rest"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// InstanceMetadataBundle wires the N-4b stack: the cache, the use case,
// the handler. Cache + UC are exposed so Story 521 (BE PUT-instance
// reconfigure hook) can call cache.InvalidateInstance directly without
// going through the handler.
type InstanceMetadataBundle struct {
	Cache   *admininfra.MetadataCache
	UseCase *authapp.InstanceMetadataUseCase
	Handler *adminrest.InstanceMetadataHandler
}

// registryLookup adapts catalogrest.InstanceRegistry → InstanceLookup.
// Reads through the registry's reload-aware Load closure on every call
// so a runtime reload immediately reflects in the use case.
type registryLookup struct {
	reg catalogrest.InstanceRegistry
}

func (r registryLookup) Lookup(name string) (int64, ports.SonarrClient, bool) {
	inst, ok := r.reg.Snapshot()[name]
	if !ok {
		return 0, nil, false
	}
	return int64(inst.Config.ID), inst.Client, true
}

// BuildInstanceMetadata constructs the cache+UC+handler trio. Pure
// construction — no I/O, no error path; matches the BuildAuth /
// BuildSonarr style.
func BuildInstanceMetadata(sonarrBundle *SonarrBundle, log *slog.Logger) *InstanceMetadataBundle {
	cache := admininfra.NewMetadataCache("")
	lookup := registryLookup{reg: sonarrBundle.InstanceReg}
	uc := authapp.NewInstanceMetadataUseCase(lookup, cache, nil)
	domainLog := sharedports.DomainLogger(log, "instance_metadata")
	handler := adminrest.NewInstanceMetadataHandler(uc, domainLog)
	return &InstanceMetadataBundle{Cache: cache, UseCase: uc, Handler: handler}
}
