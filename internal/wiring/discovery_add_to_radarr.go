package wiring

import (
	"log/slog"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// BuildDiscoveryAddToRadarr wires the R-3 AddToRadarrUseCase + handler. The
// instance lookup resolves against the radarr holder (dual-registration).
func BuildDiscoveryAddToRadarr(radarr *RadarrSyncBundle, log *slog.Logger) *discoveryrest.AddToRadarrHandler {
	domainLog := sharedports.DomainLogger(log, "discovery")
	uc := discoapp.NewAddToRadarrUseCase(radarrAddLookup{holder: radarr.RadarrHolder}, domainLog)
	return discoveryrest.NewAddToRadarrHandler(uc, domainLog)
}
