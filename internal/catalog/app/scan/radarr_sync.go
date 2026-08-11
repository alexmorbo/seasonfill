// Package scan — Ф6-R-4b radarr-sync. Mirror of sonarr_sync.go for the movie
// vertical: pull the Radarr /movie library, upsert the movies canon (COALESCE-
// guarded) + the per-instance movie_states projection. The movie-cache entry is
// written by BOTH this loop AND the radarr-webhook handler; BuildRadarrMovieCache
// + PersistRadarrMovieCache are the SINGLE shared construction + persist path so
// the two writers cannot drift (F-21, [[project_seasonfill_b13_series_detail_v2]]).
package scan

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// InstanceType discriminators — mirror arr_instance.type.
const (
	InstanceTypeSonarr = "sonarr"
	InstanceTypeRadarr = "radarr"
)

// IsRadarr reports whether a snapshot is a radarr-type instance. Empty Type
// (legacy rows / test fixtures) defaults to sonarr so the sonarr scan path is
// byte-identical. This is the scan-dispatch routing key.
func IsRadarr(snap runtime.InstanceSnapshot) bool { return snap.Type == InstanceTypeRadarr }

// PartitionInstancesByType splits a snapshot list into sonarr-type and
// radarr-type slices. Order-preserving; empty Type → sonarr.
func PartitionInstancesByType(snaps []runtime.InstanceSnapshot) (sonarr, radarr []runtime.InstanceSnapshot) {
	for _, s := range snaps {
		if IsRadarr(s) {
			radarr = append(radarr, s)
		} else {
			sonarr = append(sonarr, s)
		}
	}
	return sonarr, radarr
}

// RadarrMovieCache is the two-writer output for one Radarr movie: the canon
// stub (movies) + the per-instance projection (movie_states).
type RadarrMovieCache struct {
	Canon movie.Canon
	State movie.StateEntry
}

// BuildRadarrMovieCache is the ONE helper both radarr-sync and radarr-webhook
// call to CONSTRUCT the movie-cache entry (F-21 anti-drift point). Every field
// the cache carries is produced here. Canon is a HydrationStub COALESCE-safe
// stub — all TMDB/OMDb columns stay nil so movieUpsertAssignments preserves any
// prior enrichment. State.MovieID is 0; PersistRadarrMovieCache stamps it from
// the canon Upsert return.
func BuildRadarrMovieCache(instanceName domain.InstanceName, m ports.RadarrMovie, now time.Time) RadarrMovieCache {
	canon := movie.Canon{
		Hydration: movie.HydrationStub,
		Title:     m.Title, // NULLIF('')-guarded downstream; empty never blanks
	}
	if m.TMDBID > 0 {
		v := domain.TMDBID(m.TMDBID)
		canon.TMDBID = &v
	}
	if m.IMDBID != "" {
		v := domain.IMDBID(m.IMDBID)
		canon.IMDBID = &v
	}
	if m.Year > 0 {
		v := m.Year
		canon.Year = &v
	}
	state := movie.StateEntry{
		InstanceName:    instanceName,
		RadarrMovieID:   m.RadarrMovieID,
		TitleSlug:       m.TitleSlug,
		Monitored:       m.Monitored,
		HasFile:         m.HasFile,
		SizeOnDiskBytes: m.SizeOnDiskBytes,
		AddedToRadarr:   true,
		UpdatedAt:       now,
	}
	if m.MinimumAvailability != "" {
		v := m.MinimumAvailability
		state.Availability = &v
	}
	return RadarrMovieCache{Canon: canon, State: state}
}

// MovieCanonUpserter is the narrow movies-canon write surface — satisfied by
// *enrichpersistence.MovieRepository (COALESCE-guarded Upsert).
type MovieCanonUpserter interface {
	Upsert(ctx context.Context, c movie.Canon) (domain.MovieID, error)
}

// MovieStateUpserter is the narrow movie_states write surface. The sync passes
// the repo directly (rich Upsert); the webhook passes an adapter routing to
// UpsertStub — both satisfy this one method so PersistRadarrMovieCache stays a
// single path.
type MovieStateUpserter interface {
	Upsert(ctx context.Context, e movie.StateEntry) error
}

// PersistRadarrMovieCache is the ONE persist path both writers call: canon
// FIRST (movie_states.movie_id FKs movies.id), then the FK-stamped state row.
// Returns the resolved movies.id. F-21 anti-drift point for persist order.
func PersistRadarrMovieCache(ctx context.Context, canonW MovieCanonUpserter, stateW MovieStateUpserter, cache RadarrMovieCache) (domain.MovieID, error) {
	movieID, err := canonW.Upsert(ctx, cache.Canon)
	if err != nil {
		return 0, fmt.Errorf("persist radarr movie cache: upsert canon: %w", err)
	}
	cache.State.MovieID = movieID
	if err := stateW.Upsert(ctx, cache.State); err != nil {
		return movieID, fmt.Errorf("persist radarr movie cache: upsert state: %w", err)
	}
	return movieID, nil
}

// RadarrInstance is the radarr-side analog of scan.Instance: a radarr client +
// its snapshot. Populated ONLY from type='radarr' arr_instance rows.
type RadarrInstance struct {
	Config runtime.InstanceSnapshot
	Client ports.RadarrClient
}

// RadarrSyncDeps is the write-side dependency set for the sync loop.
type RadarrSyncDeps struct {
	Movies      MovieCanonUpserter
	MovieStates MovieStateUpserter
	Logger      *slog.Logger
}

// RadarrSyncUseCase pulls each radarr instance's /movie library and lands the
// two-writer movie cache. Mirror of scan.UseCase's atomic instance-swap. R-4b
// ships this DORMANT (no cron scheduled) — R-6 schedules RunAll once radarr
// instances exist.
type RadarrSyncUseCase struct {
	instances atomic.Pointer[[]RadarrInstance]
	deps      RadarrSyncDeps
	logger    *slog.Logger
}

func NewRadarrSyncUseCase(instances []RadarrInstance, deps RadarrSyncDeps) *RadarrSyncUseCase {
	lg := deps.Logger
	if lg == nil {
		lg = sharedports.DomainLogger(slog.Default(), "scan")
	}
	uc := &RadarrSyncUseCase{deps: deps, logger: lg}
	cp := append([]RadarrInstance(nil), instances...)
	uc.instances.Store(&cp)
	return uc
}

func (u *RadarrSyncUseCase) loadInstances() []RadarrInstance {
	p := u.instances.Load()
	if p == nil {
		return nil
	}
	return *p
}

// SwapInstances atomically replaces the radarr instance set (reload fanout).
func (u *RadarrSyncUseCase) SwapInstances(next []RadarrInstance) {
	cp := append([]RadarrInstance(nil), next...)
	u.instances.Store(&cp)
}

// RunAll syncs every radarr instance. Best-effort per instance — a ListMovies
// failure warn-logs and continues to the next. Returns nil (errors are logged,
// never abort the caller). DORMANT until R-6 schedules it.
func (u *RadarrSyncUseCase) RunAll(ctx context.Context) error {
	for _, inst := range u.loadInstances() {
		if err := ctx.Err(); err != nil {
			return err
		}
		u.syncInstance(ctx, inst)
	}
	return nil
}

// syncInstance is the per-instance movie-cache writer — the movie analog of
// SyncSeriesFromSonarr. For each Radarr movie it calls the SHARED
// BuildRadarrMovieCache + PersistRadarrMovieCache (F-21), so it writes the exact
// same cache entry the webhook writes.
func (u *RadarrSyncUseCase) syncInstance(ctx context.Context, inst RadarrInstance) {
	instName := domain.InstanceName(inst.Config.Name)
	log := u.logger.With(slog.String("instance_name", inst.Config.Name))

	movies, err := inst.Client.ListMovies(ctx)
	if err != nil {
		log.WarnContext(ctx, "sync_radarr_movies_list_failed", slog.String("error", err.Error()))
		return
	}
	now := time.Now().UTC()
	written := 0
	for _, m := range movies {
		if err := ctx.Err(); err != nil {
			return
		}
		if m.RadarrMovieID == 0 {
			continue
		}
		cache := BuildRadarrMovieCache(instName, m, now)
		if _, perr := PersistRadarrMovieCache(ctx, u.deps.Movies, u.deps.MovieStates, cache); perr != nil {
			// Best-effort per movie — a single-row failure must not abort the
			// whole instance sync (mirror season_stats per-row swallow).
			log.WarnContext(ctx, "sync_radarr_movie_cache_upsert_failed",
				slog.Int("radarr_movie_id", m.RadarrMovieID),
				slog.String("error", perr.Error()))
			continue
		}
		written++
	}
	log.InfoContext(ctx, "sync_radarr_movies_ok", slog.Int("movies", len(movies)), slog.Int("written", written))
}
