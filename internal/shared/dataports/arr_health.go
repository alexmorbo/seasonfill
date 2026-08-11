package dataports

import "context"

// ArrHealthProbe is the narrow health-probe seam shared by Sonarr and Radarr
// clients: the two methods healthcheck.Checker needs to poll an arr instance
// and label its registry entry (name + reachability). Both SonarrClient and
// RadarrClient are supersets, so they satisfy it without any extra code.
//
// Ф6-R-6b: the health checker was originally sonarr-typed
// (atomic.Pointer[[]SonarrClient]); widening its client abstraction to this
// seam lets radarr instances be health-checked through the SAME loop without
// changing sonarr behavior (a *sonarr.Client still exposes Name + SystemStatus
// exactly as before).
type ArrHealthProbe interface {
	Name() string
	SystemStatus(ctx context.Context) (SystemStatus, error)
}
