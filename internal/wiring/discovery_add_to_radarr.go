package wiring

import (
	"log/slog"

	adminpersistence "github.com/alexmorbo/seasonfill/internal/admin/persistence"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// BuildDiscoveryAddToRadarr wires the R-3 AddToRadarrUseCase + handler. The
// instance lookup resolves against the radarr holder (dual-registration).
//
// Ф8-U-2: users is the CurrentUserResolver seam (audit F-08 — Radarr lacked
// it); nil-OK disables the request gate. Returns the *use case* alongside the
// handler so bootstrap can attach the request queue to the same pointer.
//
// R-6: the sf-<user> TagResolver is built over the SAME user_instance_tags
// repository the Sonarr add uses (arr-neutral cache row per (user, instance)).
func BuildDiscoveryAddToRadarr(
	radarr *RadarrSyncBundle,
	users discoapp.CurrentUserResolver,
	persistence *PersistenceBundle,
	log *slog.Logger,
) (*discoveryrest.AddToRadarrHandler, *discoapp.AddToRadarrUseCase) {
	domainLog := sharedports.DomainLogger(log, "discovery")
	tagRepo := adminpersistence.NewUserInstanceTagRepository(persistence.DB)
	uc := discoapp.NewAddToRadarrUseCase(radarrAddLookup{holder: radarr.RadarrHolder}, domainLog).
		WithInstanceDefaults(radarrDefaultsLookup{holder: radarr.RadarrHolder}).
		WithTagResolver(discoapp.NewTagResolver(tagRepo, domainLog))
	if users != nil {
		uc = uc.WithCurrentUserResolver(users)
	}
	return discoveryrest.NewAddToRadarrHandler(uc, domainLog), uc
}
