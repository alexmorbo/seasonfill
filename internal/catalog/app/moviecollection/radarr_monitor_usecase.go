package moviecollection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// EnableMonitorRequest asks the usecase to turn ON Radarr's native monitor for
// the collection identified by TMDBCollectionID on InstanceName. Radarr's own
// numeric collection id is resolved from the list (it differs from the TMDB id).
type EnableMonitorRequest struct {
	InstanceName     domain.InstanceName
	TMDBCollectionID int
}

// ErrRadarrCollectionNotFound is returned when the Radarr instance has no
// collection with the requested tmdbId (Radarr only tracks collections for movies
// already in its library).
var ErrRadarrCollectionNotFound = errors.New("radarr collection not found for tmdb id")

// RadarrMonitorUseCase enables Radarr native collection monitoring so Radarr
// auto-adds future franchise entries, and records collections.radarr_monitored.
// DORMANT in R-5 (no REST route; R-6 wires it).
type RadarrMonitorUseCase struct {
	lookup RadarrCollectionInstanceLookup
	store  CollectionMonitorStore
	log    *slog.Logger
}

// NewRadarrMonitorUseCase panics on nil deps — init-time bug.
func NewRadarrMonitorUseCase(lookup RadarrCollectionInstanceLookup, store CollectionMonitorStore, log *slog.Logger) *RadarrMonitorUseCase {
	if lookup == nil {
		panic("NewRadarrMonitorUseCase: lookup required")
	}
	if store == nil {
		panic("NewRadarrMonitorUseCase: store required")
	}
	if log == nil {
		panic("NewRadarrMonitorUseCase: log required")
	}
	return &RadarrMonitorUseCase{lookup: lookup, store: store, log: log}
}

// EnableNativeMonitor:
//  1. resolve the per-instance Radarr client; 404 instance_not_found on miss.
//  2. GET /collection, find the row whose tmdbId == req.TMDBCollectionID;
//     ErrRadarrCollectionNotFound when absent.
//  3. if not already monitored, PUT /collection/{radarr id} with monitored=true.
//  4. record collections.radarr_monitored=true (idempotent; not-found tolerated).
func (uc *RadarrMonitorUseCase) EnableNativeMonitor(ctx context.Context, req EnableMonitorRequest) error {
	if req.TMDBCollectionID == 0 {
		return fmt.Errorf("enable native monitor: tmdb_collection_id must be non-zero")
	}
	client, ok := uc.lookup.Lookup(string(req.InstanceName))
	if !ok {
		return errors.Join(&sharedErrors.InstanceNotFoundError{Name: req.InstanceName}, ports.ErrNotFound)
	}

	cols, err := client.GetCollections(ctx)
	if err != nil {
		return fmt.Errorf("enable native monitor: list radarr collections: %w", err)
	}
	var found *ports.RadarrCollection
	for i := range cols {
		if cols[i].TMDBID == req.TMDBCollectionID {
			found = &cols[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("enable native monitor (tmdb=%d): %w", req.TMDBCollectionID, ErrRadarrCollectionNotFound)
	}

	if !found.Monitored {
		updated := *found
		updated.Monitored = true
		if err := client.PutCollection(ctx, updated); err != nil {
			return fmt.Errorf("enable native monitor: put radarr collection %d: %w", found.ID, err)
		}
	}

	if err := uc.store.SetRadarrMonitored(ctx, req.TMDBCollectionID, true); err != nil && !errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("enable native monitor: persist flag: %w", err)
	}

	uc.log.InfoContext(ctx, "moviecollection.radarr_monitor.enabled",
		slog.Int("collection_id", req.TMDBCollectionID),
		slog.Int("radarr_collection_id", found.ID),
		slog.String("instance", string(req.InstanceName)),
	)
	return nil
}
