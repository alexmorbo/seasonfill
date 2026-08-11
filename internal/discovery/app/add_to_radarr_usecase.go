package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// AddRadarrInstanceLookup resolves an instance by operator-visible name to
// its per-instance Radarr client. Mirrors AddInstanceLookup (Sonarr). Ф6-R-3.
type AddRadarrInstanceLookup interface {
	Lookup(name string) (client ports.RadarrClient, ok bool)
}

// AddMovieRequest is the add-to-Radarr use-case input. MinimumAvailability is
// a per-add override — "" defers to the client default ("released", ADR-0018
// Q3). R-3 keeps the movie add-flow tag-less; user-tag parity is deferred to
// R-6 (§10a) to avoid widening TagResolver to a second client type here.
type AddMovieRequest struct {
	InstanceName        domain.InstanceName
	TMDBID              int
	QualityProfileID    int
	RootFolderPath      string
	Monitored           bool
	MinimumAvailability string // "" ⇒ "released" (client default); per-add override
	SearchOnAdd         bool
	Username            string // reserved for R-6 user-tag parity; unused in R-3
}

// AddMovieResult is the add-to-Radarr use-case output. AlreadyAdded is true
// when Radarr rejected a duplicate tmdbId (idempotent success). UserTag* stay
// zero in R-3 (tag-less; R-6 wires user-tag parity).
type AddMovieResult struct {
	RadarrMovieID int
	InstanceName  domain.InstanceName
	AlreadyAdded  bool
	UserTagLabel  string
	UserTagID     int
}

// AddToRadarrUseCase orchestrates the discovery "Add to Radarr" flow. It is
// the movie analog of AddToSonarrUseCase, minus the tag resolution (R-3 is
// tag-less — see §10a). No REST route is wired in R-3 (that is R-6); the use
// case is exercised via unit tests only.
type AddToRadarrUseCase struct {
	lookup AddRadarrInstanceLookup
	log    *slog.Logger
}

// NewAddToRadarrUseCase panics on nil deps — init-time bug.
func NewAddToRadarrUseCase(lookup AddRadarrInstanceLookup, log *slog.Logger) *AddToRadarrUseCase {
	if lookup == nil {
		panic("NewAddToRadarrUseCase: lookup required")
	}
	if log == nil {
		panic("NewAddToRadarrUseCase: log required")
	}
	return &AddToRadarrUseCase{lookup: lookup, log: log}
}

// Add executes the add-to-Radarr flow:
//
//  1. Lookup the per-instance Radarr client; 404 instance_not_found on miss.
//  2. GET /api/v3/movie/lookup?term=tmdb:{id} to resolve title/slug/year/images
//     (Radarr rejects POST /api/v3/movie without them). Empty result → 404.
//  3. POST /api/v3/movie. A duplicate tmdbId (Radarr 400 MovieExistsValidator)
//     is treated as an idempotent success (AlreadyAdded=true), not an error.
//     Network/other failures → 502 radarr_unreachable.
func (uc *AddToRadarrUseCase) Add(ctx context.Context, req AddMovieRequest) (AddMovieResult, error) {
	client, ok := uc.lookup.Lookup(string(req.InstanceName))
	if !ok {
		return AddMovieResult{}, errors.Join(
			&sharedErrors.InstanceNotFoundError{Name: req.InstanceName},
			ports.ErrNotFound,
		)
	}

	payload := ports.AddMoviePayload{
		TMDBID:              req.TMDBID,
		QualityProfileID:    req.QualityProfileID,
		RootFolderPath:      req.RootFolderPath,
		Monitored:           req.Monitored,
		MinimumAvailability: req.MinimumAvailability, // client defaults "" → "released"
		SearchOnAdd:         req.SearchOnAdd,
	}

	results, err := client.LookupMovie(ctx, fmt.Sprintf("tmdb:%d", req.TMDBID))
	if err != nil {
		return AddMovieResult{}, &sharedErrors.RadarrUnreachableError{
			Instance: req.InstanceName,
			Cause:    fmt.Errorf("lookup movie: %w", err),
		}
	}
	if len(results) == 0 {
		return AddMovieResult{}, errors.Join(
			&sharedErrors.InstanceNotFoundError{Name: req.InstanceName},
			ports.ErrNotFound,
		)
	}

	found := results[0]
	payload.Title = found.Title
	payload.TitleSlug = found.TitleSlug
	payload.Year = found.Year
	payload.Images = found.Images

	res, err := client.AddMovie(ctx, payload)
	if err != nil {
		if isMovieAlreadyAdded(err) {
			uc.log.InfoContext(ctx, "add_to_radarr_already_added",
				slog.String("instance", string(req.InstanceName)),
				slog.Int("tmdb_id", req.TMDBID))
			return AddMovieResult{
				InstanceName: req.InstanceName,
				AlreadyAdded: true,
			}, nil
		}
		return AddMovieResult{}, &sharedErrors.RadarrUnreachableError{
			Instance: req.InstanceName,
			Cause:    fmt.Errorf("add movie: %w", err),
		}
	}

	return AddMovieResult{
		RadarrMovieID: res.RadarrMovieID,
		InstanceName:  req.InstanceName,
	}, nil
}

// isMovieAlreadyAdded inspects a Radarr StatusError body for the duplicate
// signal. Radarr returns HTTP 400 with a body containing "MovieExistsValidator"
// (or "This movie has already been added") when the tmdbId is already in the
// library — the use case maps that to an idempotent already-added result.
func isMovieAlreadyAdded(err error) bool {
	var se *arrcore.StatusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Status != 400 {
		return false
	}
	b := strings.ToLower(se.Body)
	return strings.Contains(b, "movieexistsvalidator") || strings.Contains(b, "already been added")
}
