package wiring

import (
	"context"
	"errors"
	"log/slog"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admininfra "github.com/alexmorbo/seasonfill/internal/admin/infrastructure"
	adminrest "github.com/alexmorbo/seasonfill/internal/admin/rest"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// InstanceMetadataBundle wires the N-4b stack: the cache, the use case,
// the handler. Cache + UC are exposed so Story 521 (BE PUT-instance
// reconfigure hook) can call cache.InvalidateInstance directly without
// going through the handler.
type InstanceMetadataBundle struct {
	Cache   *admininfra.MetadataCache
	UseCase *authapp.InstanceMetadataUseCase
	Handler *adminrest.InstanceMetadataHandler
}

// registryLookup adapts catalogrest.InstanceRegistry → InstanceLookup.
// Reads through the registry's reload-aware Load closure on every call
// so a runtime reload immediately reflects in the use case.
type registryLookup struct {
	reg catalogrest.InstanceRegistry
}

func (r registryLookup) Lookup(name string) (int64, ports.SonarrClient, bool) {
	inst, ok := r.reg.Snapshot()[name]
	if !ok {
		return 0, nil, false
	}
	return int64(inst.Config.ID), inst.Client, true
}

// TMDBSeasonsClient is the narrow TMDB surface the resolver consumes.
// Both methods are already implemented by cmd/server/adapters.TMDBClientHolder
// — the holder is reload-swappable so the resolver stays valid across
// runtime config changes. Story 525.
type TMDBSeasonsClient interface {
	GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error)
	FindByTVDB(ctx context.Context, tvdbID domain.TVDBID) (*tmdb.FindResponse, error)
}

// SeriesCanonReader is the narrow canon read used by the resolver to
// short-circuit on rows we already have in the local catalog. Matches
// the *SeriesRepository.FindByExternalIDs signature.
type SeriesCanonReader interface {
	FindByExternalIDs(ctx context.Context, tmdbID *domain.TMDBID, tvdbID *domain.TVDBID, imdbID *domain.IMDBID) (series.Canon, error)
}

// SeasonsCatalogReader is the narrow seasons read used by the resolver.
// Matches *SeasonsRepository.ListBySeries signature.
type SeasonsCatalogReader interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonSeason, error)
}

// tmdbSeasonsResolver implements authapp.SeasonsResolver. Chain:
//  1. local catalog (canon by tvdb_id, hydration=full + seasons rows
//     already persisted) — zero network round-trips,
//  2. TMDB GetTV(tmdbHint) — used when canon row exists but seasons
//     aren't persisted, OR when tmdbHint is supplied directly by
//     Sonarr's lookup,
//  3. TMDB FindByTVDB → GetTV(found id) — last resort when neither
//     canon nor Sonarr knows the TMDB id.
//
// Empty slice (no error) signals "no authoritative seasons available";
// the caller keeps Sonarr's seasons[]. Non-fatal upstream errors are
// logged at debug level and surfaced as empty slice for the same
// reason — the lookup endpoint MUST stay best-effort.
type tmdbSeasonsResolver struct {
	canon   SeriesCanonReader
	seasons SeasonsCatalogReader
	tmdb    TMDBSeasonsClient
	log     *slog.Logger
}

func (r *tmdbSeasonsResolver) ResolveSeasons(ctx context.Context, tvdbID, tmdbHint int) ([]ports.SeasonInfo, error) {
	tvdb := domain.TVDBID(tvdbID)
	var tmdbID *domain.TMDBID
	if tmdbHint > 0 {
		t := domain.TMDBID(tmdbHint)
		tmdbID = &t
	}

	// Step 1: local catalog. FindByExternalIDs probes tmdb_id first, then
	// tvdb_id — we hand both so a row known by either external id wins.
	if r.canon != nil && r.seasons != nil {
		canon, err := r.canon.FindByExternalIDs(ctx, tmdbID, &tvdb, nil)
		switch {
		case err == nil:
			// Promote canon-side TMDB id into the hint so step 2 skips
			// the FindByTVDB round-trip if step 1 doesn't satisfy.
			if canon.TMDBID != nil && tmdbHint <= 0 {
				tmdbHint = int(*canon.TMDBID)
			}
			if canon.Hydration == series.HydrationFull && canon.ID != 0 {
				rows, sErr := r.seasons.ListBySeries(ctx, canon.ID)
				if sErr == nil && len(rows) > 0 {
					return canonSeasonsToInfo(rows), nil
				}
			}
		case !errors.Is(err, ports.ErrNotFound):
			if r.log != nil {
				r.log.Debug("seasons resolver: canon lookup failed", "tvdb_id", tvdbID, "err", err)
			}
		}
	}

	// Step 2 & 3: TMDB GetTV. Resolve tmdb_id via FindByTVDB when we
	// still don't know it.
	if r.tmdb == nil {
		return nil, nil
	}
	if tmdbHint <= 0 {
		fr, err := r.tmdb.FindByTVDB(ctx, tvdb)
		if err != nil {
			if r.log != nil {
				r.log.Debug("seasons resolver: tmdb FindByTVDB failed", "tvdb_id", tvdbID, "err", err)
			}
			return nil, nil
		}
		if len(fr.TVResults) == 0 {
			return nil, nil
		}
		tmdbHint = int(fr.TVResults[0].ID)
	}
	tv, err := r.tmdb.GetTV(ctx, int64(tmdbHint), "")
	if err != nil {
		if r.log != nil {
			r.log.Debug("seasons resolver: tmdb GetTV failed", "tmdb_id", tmdbHint, "err", err)
		}
		return nil, nil
	}
	return tvSeasonsToInfo(tv.Seasons), nil
}

// canonSeasonsToInfo projects catalog seasons rows into the lookup
// season DTO. NULL EpisodeCount surfaces as 0 — the FE collapses zero-
// count rows by default so a still-loading season degrades gracefully.
func canonSeasonsToInfo(rows []series.CanonSeason) []ports.SeasonInfo {
	out := make([]ports.SeasonInfo, 0, len(rows))
	for _, s := range rows {
		ec := 0
		if s.EpisodeCount != nil {
			ec = *s.EpisodeCount
		}
		out = append(out, ports.SeasonInfo{
			SeasonNumber: s.SeasonNumber,
			EpisodeCount: ec,
		})
	}
	return out
}

// tvSeasonsToInfo projects TMDB /tv/{id}.seasons[*] stubs into the
// lookup season DTO. Drop nothing — Sonarr's lookup also returns the
// Season 0 specials row; the FE filters as it sees fit.
func tvSeasonsToInfo(stubs []tmdb.TVSeasonStub) []ports.SeasonInfo {
	out := make([]ports.SeasonInfo, 0, len(stubs))
	for _, s := range stubs {
		out = append(out, ports.SeasonInfo{
			SeasonNumber: s.SeasonNumber,
			EpisodeCount: s.EpisodeCount,
		})
	}
	return out
}

// BuildInstanceMetadata constructs the cache+UC+handler trio. Pure
// construction — no I/O, no error path; matches the BuildAuth /
// BuildSonarr style.
//
// Story 525: persistence + tmdb are optional. When both are non-nil the
// use case installs a SeasonsResolver that prefers our catalog / TMDB
// for per-season episode_count (Sonarr returns 0 for not-yet-added
// series); when either is nil the lookup falls back to Sonarr's
// seasons unchanged. The tests that wire neither (legacy + admin REST
// tests) keep the original Sonarr-only behavior.
func BuildInstanceMetadata(sonarrBundle *SonarrBundle, persistence *PersistenceBundle, tmdbClient TMDBSeasonsClient, log *slog.Logger) *InstanceMetadataBundle {
	cache := admininfra.NewMetadataCache("")
	lookup := registryLookup{reg: sonarrBundle.InstanceReg}
	uc := authapp.NewInstanceMetadataUseCase(lookup, cache, nil)
	domainLog := sharedports.DomainLogger(log, "admin")
	if persistence != nil && persistence.DB != nil && tmdbClient != nil {
		uc.WithSeasonsResolver(&tmdbSeasonsResolver{
			canon:   enrichpersistence.NewSeriesRepository(persistence.DB),
			seasons: enrichpersistence.NewSeasonsRepository(persistence.DB),
			tmdb:    tmdbClient,
			log:     domainLog,
		})
	}
	handler := adminrest.NewInstanceMetadataHandler(uc, domainLog)
	return &InstanceMetadataBundle{Cache: cache, UseCase: uc, Handler: handler}
}
