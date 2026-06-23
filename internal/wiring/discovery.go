package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	discopersistence "github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// discovery.go wires the discovery bounded-context persistence
// + cross-context adapter set (PRD §5.1.1 / story 505 N-2d).
//
// The vertical-slice rule (PRD §3.3) forbids internal/discovery/ from
// importing internal/enrichment/ directly. The StubUpserter adapter
// lives here in the wiring package precisely to bridge that boundary:
// wiring is allowed to import every context, so it can compose a
// discoapp.StubUpserter from
// enrichpersistence.SeriesRepository.UpsertStub.
//
// Future stories (506 worker, 507 handlers) call BuildDiscoveryPersistence
// from server.go and read through the returned bundle.

// DiscoveryPersistenceBundle groups the discovery persistence + adapters
// constructed at boot. Story 505 ships ListRepo + LangProvider + Stubs;
// story 506 adds the worker and consumes all three.
type DiscoveryPersistenceBundle struct {
	ListRepo     *discopersistence.ListRepository
	LangProvider *discopersistence.ActiveLanguagesRepository
	Stubs        discoapp.StubUpserter
}

// BuildDiscoveryPersistence wires the three discovery persistence
// components.
//
// The signature accepts the enrichment SeriesRepository (NOT the
// kernel DB handle) so the adapter wraps an existing repo pointer
// rather than re-constructing one. Server.go calls this AFTER it has
// built the enrichment bundle.
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers (room for
// future seed-or-validate logic).
func BuildDiscoveryPersistence(
	persistence *PersistenceBundle,
	seriesRepo *enrichpersistence.SeriesRepository,
) (*DiscoveryPersistenceBundle, error) {
	db := persistence.DB
	return &DiscoveryPersistenceBundle{
		ListRepo:     discopersistence.NewListRepository(db),
		LangProvider: discopersistence.NewActiveLanguagesRepository(db),
		Stubs:        &stubUpserterAdapter{seriesRepo: seriesRepo},
	}, nil
}

// DiscoveryRuntimeBundle groups the worker + supporting reader
// constructed at boot. Server.go consumes Worker via the
// cmd/server/loops/discovery.go entry point.
type DiscoveryRuntimeBundle struct {
	Worker   *discoapp.Worker
	TopKinds *discopersistence.TopKindsReader
}

// realDiscoveryClock satisfies discoapp.Clock with time.Now().
type realDiscoveryClock struct{}

func (realDiscoveryClock) Now() time.Time { return time.Now() }

// DiscoveryRuntimeDeps is the input contract for BuildDiscoveryRuntime.
// All fields required — nil causes a wiring error before NewWorker is
// reached.
type DiscoveryRuntimeDeps struct {
	Persistence *DiscoveryPersistenceBundle
	DB          *gorm.DB
	TMDB        discoapp.TMDBClient
	Log         *slog.Logger
}

// BuildDiscoveryRuntime wires the worker + top-kinds reader. The
// caller is server.go, which:
//  1. invokes BuildDiscoveryPersistence first to get the repos +
//     stub adapter,
//  2. passes the live TMDBClientHolder (cmd/server/adapters) as the
//     discoapp.TMDBClient,
//  3. starts the loop via cmd/server/loops.RunDiscovery on
//     lifecycle.Go.
//
// The Log argument MUST already carry the "discovery" domain tag —
// callers should pass sharedports.DomainLogger(log, "discovery").
// The construction is in-memory only; an error path is returned for
// symmetry with the sibling Build* wirers (room for future
// boot-time validation).
func BuildDiscoveryRuntime(deps DiscoveryRuntimeDeps) (*DiscoveryRuntimeBundle, error) {
	if deps.Persistence == nil {
		return nil, fmt.Errorf("discovery runtime: persistence required")
	}
	if deps.DB == nil {
		return nil, fmt.Errorf("discovery runtime: db required")
	}
	if deps.TMDB == nil {
		return nil, fmt.Errorf("discovery runtime: tmdb client required")
	}
	if deps.Log == nil {
		return nil, fmt.Errorf("discovery runtime: log required")
	}
	topKinds := discopersistence.NewTopKindsReader(deps.DB)
	worker := discoapp.NewWorker(discoapp.WorkerDeps{
		Repo:     deps.Persistence.ListRepo,
		Langs:    deps.Persistence.LangProvider,
		Stubs:    deps.Persistence.Stubs,
		TMDB:     deps.TMDB,
		TopKinds: topKinds,
		Log:      deps.Log,
		Clock:    realDiscoveryClock{},
	})
	return &DiscoveryRuntimeBundle{
		Worker:   worker,
		TopKinds: topKinds,
	}, nil
}

// stubUpserterAdapter bridges enrichpersistence.SeriesRepository.UpsertStub
// (which takes the rich series.Canon) to the narrow discoapp.StubUpserter
// port (which takes only the tmdb_id + title + poster + backdrop a
// discovery worker has on hand from a TMDB Trending/Popular response).
//
// The adapter materialises a stub Canon with Hydration="stub" so the
// COALESCE-protected UpsertStub path correctly merges against any pre-
// existing row without downgrading a 'full' hydration to 'stub'
// (see SeriesRepository.UpsertStub godoc for the merge invariants).
type stubUpserterAdapter struct {
	seriesRepo *enrichpersistence.SeriesRepository
}

func (a *stubUpserterAdapter) EnsureStub(
	ctx context.Context,
	tmdbID shareddomain.TMDBID,
	title string,
	poster, backdrop *string,
) (shareddomain.SeriesID, error) {
	if title == "" {
		return 0, fmt.Errorf("discovery stub upserter: title required")
	}
	// Copy tmdbID into a local before taking its address so the pointer
	// in series.Canon does not alias the caller's parameter slot — the
	// adapter must own the lifetime of the value it writes through.
	tmdbCopy := tmdbID
	canon := series.Canon{
		TMDBID:        &tmdbCopy,
		Title:         title,
		Hydration:     series.HydrationStub,
		PosterAsset:   poster,
		BackdropAsset: backdrop,
	}
	return a.seriesRepo.UpsertStub(ctx, canon)
}
