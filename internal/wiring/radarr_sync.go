package wiring

import (
	"context"
	"log/slog"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
)

// stubMovieStateWriter routes scan.MovieStateUpserter.Upsert onto the thin
// MovieStatesRepository.UpsertStub — used by the radarr-webhook (L3-2) so a
// stat-less webhook write can't zero a real cached stat written by the sync.
// The sync path passes the repo directly (rich Upsert). Single definition so
// the two thin-writer call sites can't drift.
type stubMovieStateWriter struct {
	repo *catalogpersistence.MovieStatesRepository
}

func (w stubMovieStateWriter) Upsert(ctx context.Context, e movie.StateEntry) error {
	return w.repo.UpsertStub(ctx, e)
}

// RadarrSyncBundle groups the radarr-sync usecase + its repos so the webhook
// wiring (L3-2) can reuse the SAME MovieStatesRepository + MovieRepository.
type RadarrSyncBundle struct {
	SyncUC      *scan.RadarrSyncUseCase
	MovieStates *catalogpersistence.MovieStatesRepository
	Movies      *enrichpersistence.MovieRepository
}

// BuildRadarrSync constructs the DORMANT radarr-sync usecase (no cron). The
// fanout feeds SwapInstances from the radarr partition; RunAll is scheduled by
// R-6. Movies + MovieStates repos are shared with the L3-2 webhook.
func BuildRadarrSync(db *gorm.DB, log *slog.Logger) *RadarrSyncBundle {
	movies := enrichpersistence.NewMovieRepository(db)
	states := catalogpersistence.NewMovieStatesRepository(db)
	uc := scan.NewRadarrSyncUseCase(nil, scan.RadarrSyncDeps{
		Movies:      movies,
		MovieStates: states, // rich Upsert
		Logger:      log,
	})
	return &RadarrSyncBundle{SyncUC: uc, MovieStates: states, Movies: movies}
}
