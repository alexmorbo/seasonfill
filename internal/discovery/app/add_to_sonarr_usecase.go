package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// AddInstanceLookup resolves an instance by operator-visible name to its
// per-instance Sonarr client. Satisfied by the same registry adapter
// used by the N-4b instance metadata use case (wiring closure).
type AddInstanceLookup interface {
	Lookup(name string) (client ports.SonarrClient, ok bool)
}

// CurrentUserResolver returns the authenticated user for the request.
// nil result with nil error == bypass / api-key / disabled-auth session;
// the caller treats nil as "sf-system" downstream.
type CurrentUserResolver interface {
	GetCurrent(ctx context.Context, username string) (*admin.User, error)
}

// AddRequest is the use-case input (handler decodes from JSON).
//
// MonitoredSeasons (story 524 N-4 per-season picker) is the explicit
// list of season numbers the operator chose in the modal. nil/empty
// means "no per-season override" — MonitorMode governs alone. When
// non-empty, the use case calls Sonarr's lookup endpoint to discover
// the full season list and stamps `monitored=true` on the chosen
// numbers + `false` on the rest, then forwards the explicit array on
// the add payload.
type AddRequest struct {
	InstanceName     domain.InstanceName
	TVDBID           int
	QualityProfileID int
	RootFolderPath   string
	Monitored        bool
	MonitorMode      string
	SearchOnAdd      bool
	Username         string // empty → bypass / system
	MonitoredSeasons []int
}

// AddResult is the use-case output.
type AddResult struct {
	SonarrSeriesID int
	InstanceName   domain.InstanceName
	UserTagLabel   string // "sf-alex" or "" when tag resolve failed
	UserTagID      int    // 0 = no tag (resolve failed or skipped)
}

// AddToSonarrUseCase orchestrates the discovery "Add to Sonarr" flow.
type AddToSonarrUseCase struct {
	lookup   AddInstanceLookup
	users    CurrentUserResolver
	resolver *TagResolver
	log      *slog.Logger
}

// NewAddToSonarrUseCase panics on nil deps — init-time bug.
func NewAddToSonarrUseCase(lookup AddInstanceLookup, users CurrentUserResolver, resolver *TagResolver, log *slog.Logger) *AddToSonarrUseCase {
	if lookup == nil {
		panic("NewAddToSonarrUseCase: lookup required")
	}
	if users == nil {
		panic("NewAddToSonarrUseCase: users required")
	}
	if resolver == nil {
		panic("NewAddToSonarrUseCase: resolver required")
	}
	if log == nil {
		panic("NewAddToSonarrUseCase: log required")
	}
	return &AddToSonarrUseCase{lookup: lookup, users: users, resolver: resolver, log: log}
}

// Add executes the add-to-sonarr flow per PRD lines 5127-5163.
//
//  1. Lookup the per-instance Sonarr client; 404 instance_not_found on miss.
//  2. Resolve the current user (nil for bypass).
//  3. Resolve the user tag — tag failures are non-blocking; the series
//     is added without a tag, with a WARN log + empty UserTagLabel.
//  4. POST /api/v3/series. Network/5xx → 502 sonarr_unreachable.
func (uc *AddToSonarrUseCase) Add(ctx context.Context, req AddRequest) (AddResult, error) {
	client, ok := uc.lookup.Lookup(string(req.InstanceName))
	if !ok {
		return AddResult{}, errors.Join(
			&sharedErrors.InstanceNotFoundError{Name: req.InstanceName},
			ports.ErrNotFound,
		)
	}

	var user *admin.User
	if req.Username != "" {
		u, err := uc.users.GetCurrent(ctx, req.Username)
		if err == nil {
			user = u
		} else {
			// Soft fail — bypass-style flow stays available.
			uc.log.WarnContext(ctx, "add_to_sonarr_user_resolve_failed",
				slog.String("username", req.Username),
				slog.String("error", err.Error()))
		}
	}

	tagID, tagLabel, tagErr := uc.resolver.Resolve(ctx, client, user, req.InstanceName)
	if tagErr != nil {
		uc.log.WarnContext(ctx, "add_to_sonarr_tag_resolve_failed",
			slog.String("instance", string(req.InstanceName)),
			slog.String("error", tagErr.Error()))
		tagID = 0
		tagLabel = ""
	}

	payload := ports.AddSeriesPayload{
		TVDBID:           req.TVDBID,
		QualityProfileID: req.QualityProfileID,
		RootFolderPath:   req.RootFolderPath,
		Monitored:        req.Monitored,
		MonitorMode:      req.MonitorMode,
		SearchOnAdd:      req.SearchOnAdd,
	}
	if tagID > 0 {
		payload.Tags = []int{tagID}
	}

	if len(req.MonitoredSeasons) > 0 {
		results, err := client.LookupSeries(ctx, fmt.Sprintf("tvdb:%d", req.TVDBID))
		if err != nil {
			return AddResult{}, &sharedErrors.SonarrUnreachableError{
				Instance: req.InstanceName,
				Cause:    fmt.Errorf("lookup series: %w", err),
			}
		}
		if len(results) == 0 {
			// Sonarr's metadata provider returned no rows for the TVDB id.
			// Surface as not_found via the typed instance error joined to
			// ErrNotFound — the handler maps to 404 via F-2c.
			return AddResult{}, errors.Join(
				&sharedErrors.InstanceNotFoundError{Name: req.InstanceName},
				ports.ErrNotFound,
			)
		}
		wanted := make(map[int]bool, len(req.MonitoredSeasons))
		for _, n := range req.MonitoredSeasons {
			wanted[n] = true
		}
		seasons := make([]ports.SeasonSelection, 0, len(results[0].Seasons))
		for _, s := range results[0].Seasons {
			seasons = append(seasons, ports.SeasonSelection{
				SeasonNumber: s.SeasonNumber,
				Monitored:    wanted[s.SeasonNumber],
			})
		}
		payload.Seasons = seasons
	}
	res, err := client.AddSeries(ctx, payload)
	if err != nil {
		return AddResult{}, &sharedErrors.SonarrUnreachableError{
			Instance: req.InstanceName,
			Cause:    fmt.Errorf("add series: %w", err),
		}
	}

	return AddResult{
		SonarrSeriesID: res.SonarrSeriesID,
		InstanceName:   req.InstanceName,
		UserTagLabel:   tagLabel,
		UserTagID:      tagID,
	}, nil
}
