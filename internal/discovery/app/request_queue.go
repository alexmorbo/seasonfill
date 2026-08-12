package app

import (
	"context"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
)

// RequestQueue is the Ф8-U-2 permission-gated add queue. Implemented by
// *request/app.UseCase; the discovery add use cases hold it nil-OK. Queue is
// idempotent on (userID, spec.MediaType, spec.ExternalID) among pending rows —
// a second call returns the existing pending request id.
type RequestQueue interface {
	Queue(ctx context.Context, userID uint, spec reqdomain.AddSpec) (requestID int64, err error)
}

// autoApproves reports whether the user may add directly (admin short-circuit
// OR the auto_approve permission). Mirrors the middleware ADMIN short-circuit.
func autoApproves(u *admin.User) bool {
	return u.Role == admin.RoleAdmin || u.AutoApprove
}

// tvAddSpec snapshots a Sonarr AddRequest into a replayable AddSpec.
func tvAddSpec(req AddRequest) reqdomain.AddSpec {
	return reqdomain.AddSpec{
		MediaType:        reqdomain.MediaTypeTV,
		ExternalID:       int64(req.TVDBID),
		InstanceName:     string(req.InstanceName),
		QualityProfileID: req.QualityProfileID,
		RootFolderPath:   req.RootFolderPath,
		Monitored:        req.Monitored,
		MonitorMode:      req.MonitorMode,
		SearchOnAdd:      req.SearchOnAdd,
		Seasons:          req.MonitoredSeasons,
	}
}

// movieAddSpec snapshots a Radarr AddMovieRequest into a replayable AddSpec.
func movieAddSpec(req AddMovieRequest) reqdomain.AddSpec {
	return reqdomain.AddSpec{
		MediaType:           reqdomain.MediaTypeMovie,
		ExternalID:          int64(req.TMDBID),
		InstanceName:        string(req.InstanceName),
		QualityProfileID:    req.QualityProfileID,
		RootFolderPath:      req.RootFolderPath,
		Monitored:           req.Monitored,
		MinimumAvailability: req.MinimumAvailability,
		SearchOnAdd:         req.SearchOnAdd,
	}
}
