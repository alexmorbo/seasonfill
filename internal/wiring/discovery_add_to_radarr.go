package wiring

import (
	"log/slog"

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
func BuildDiscoveryAddToRadarr(radarr *RadarrSyncBundle, users discoapp.CurrentUserResolver, log *slog.Logger) (*discoveryrest.AddToRadarrHandler, *discoapp.AddToRadarrUseCase) {
	domainLog := sharedports.DomainLogger(log, "discovery")
	uc := discoapp.NewAddToRadarrUseCase(radarrAddLookup{holder: radarr.RadarrHolder}, domainLog).
		WithInstanceDefaults(radarrDefaultsLookup{holder: radarr.RadarrHolder})
	if users != nil {
		uc = uc.WithCurrentUserResolver(users)
	}
	return discoveryrest.NewAddToRadarrHandler(uc, domainLog), uc
}
