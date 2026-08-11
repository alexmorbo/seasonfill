// Package moviecollection holds the Ф6-R-5 movie-franchise usecases: adding a
// collection's missing parts to a Radarr instance, and enabling Radarr's native
// collection monitor. Both are DORMANT in R-5 (no REST route — R-6 wires the
// buttons) but ship fully unit-tested behind narrow ports. Distinct from the Ф7
// insight-collections package (internal/catalog/app/collections).
package moviecollection

import (
	"context"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// AddMovieRequest is the neutral add-one-movie input the add-all-missing usecase
// hands the MovieAdder for each missing part. It mirrors the R-3
// discovery/app.AddMovieRequest fields the batch needs; the wiring adapter (R-6)
// translates it to the concrete AddToRadarrUseCase. Keeping it local decouples
// this package from internal/discovery/app.
type AddMovieRequest struct {
	InstanceName        domain.InstanceName
	TMDBID              int
	QualityProfileID    int
	RootFolderPath      string
	Monitored           bool
	MinimumAvailability string // "" ⇒ Radarr client default ("released")
	SearchOnAdd         bool
}

// AddMovieOutcome is the neutral per-add result. AlreadyAdded mirrors the R-3
// idempotent already-present signal.
type AddMovieOutcome struct {
	RadarrMovieID int
	AlreadyAdded  bool
}

// MovieAdder is the narrow add-to-Radarr seam. Production impl (R-6) adapts
// *discoveryapp.AddToRadarrUseCase.Add. Tests pass a fake.
type MovieAdder interface {
	Add(ctx context.Context, req AddMovieRequest) (AddMovieOutcome, error)
}

// RadarrCollectionClient is the narrow Radarr native-collection seam. Production
// impl: *radarr.Client (GetCollections + PutCollection). Tests pass a fake.
type RadarrCollectionClient interface {
	GetCollections(ctx context.Context) ([]ports.RadarrCollection, error)
	PutCollection(ctx context.Context, col ports.RadarrCollection) error
}

// RadarrCollectionInstanceLookup resolves an operator-visible instance name to
// its per-instance RadarrCollectionClient. Mirror of the R-3
// AddRadarrInstanceLookup shape.
type RadarrCollectionInstanceLookup interface {
	Lookup(name string) (client RadarrCollectionClient, ok bool)
}

// CollectionMonitorStore persists the radarr_monitored flag. Production impl:
// *enrichpersistence.MovieCollectionsRepository.SetRadarrMonitored.
type CollectionMonitorStore interface {
	SetRadarrMonitored(ctx context.Context, tmdbCollectionID int, monitored bool) error
}
