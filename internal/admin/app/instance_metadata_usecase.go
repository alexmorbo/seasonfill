package auth

import (
	"context"
	"errors"
	"strconv"
	"time"

	admininfra "github.com/alexmorbo/seasonfill/internal/admin/infrastructure"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// InstanceLookup resolves a runtime instance by its operator-visible
// name. The wiring adapts catalogrest.InstanceRegistry into this; the
// use case stays free of any rest-layer import.
type InstanceLookup interface {
	Lookup(name string) (client ports.SonarrClient, ok bool)
}

// QPRFClient is the narrowest client surface the quality-profiles /
// root-folders endpoints need. Both ports.SonarrClient and ports.RadarrClient
// are supersets (the endpoints are byte-identical across arrs via arrcore), so
// the metadata UC can serve QP/RF for either arr kind without depending on the
// full SonarrClient. Ф6-R-6b Gap 2c.
type QPRFClient interface {
	ListQualityProfiles(ctx context.Context) ([]ports.QualityProfile, error)
	ListRootFolders(ctx context.Context) ([]ports.RootFolder, error)
}

// RadarrQPRFLookup resolves a radarr instance name to its QP/RF client. Wired
// over the reload-aware radarr holder; nil when radarr is unwired (minimal/
// test wirings) → QP/RF stay sonarr-only. Ф6-R-6b Gap 2c.
type RadarrQPRFLookup interface {
	Lookup(name string) (client QPRFClient, ok bool)
}

// CacheStatus values for metadata responses.
const (
	CacheStatusHit  = "hit"
	CacheStatusMiss = "miss"
)

// QualityProfilesResult is the use case's quality-profiles response.
type QualityProfilesResult struct {
	Items        []ports.QualityProfile
	RefreshedAt  time.Time
	CacheStatus  string
	InstanceName string
}

// RootFoldersResult mirrors QualityProfilesResult for /root-folders.
type RootFoldersResult struct {
	Items        []ports.RootFolder
	RefreshedAt  time.Time
	CacheStatus  string
	InstanceName string
}

// SonarrLookupResult is the use case's lookup response. Story 524 N-4
// per-season picker — uncached; the FE calls this once per modal open
// and React Query handles client-side caching.
type SonarrLookupResult struct {
	Items        []ports.SonarrLookupResult
	InstanceName string
}

// SeasonsResolver resolves a tvdb_id (and best-effort tmdb_id hint) to
// the authoritative per-season episode_count list. Story 525 — TMDB /
// catalog are the source of truth; Sonarr's lookup returns
// `episode_count=0` for not-yet-added series whose TVDB metadata cache
// is incomplete. Implementations chain catalog seasons → TMDB GetTV →
// TMDB FindByTVDB. Returns the empty slice + nil when neither source
// has data; callers fall back to the Sonarr-supplied seasons.
type SeasonsResolver interface {
	ResolveSeasons(ctx context.Context, tvdbID, tmdbHint int) ([]ports.SeasonInfo, error)
}

// InstanceMetadataUseCase drives the three N-4b endpoints.
type InstanceMetadataUseCase struct {
	lookup  InstanceLookup
	radarr  RadarrQPRFLookup // optional — Ф6-R-6b QP/RF radarr fallback
	cache   *admininfra.MetadataCache
	clock   func() time.Time
	seasons SeasonsResolver // optional — nil falls back to Sonarr seasons
}

// NewInstanceMetadataUseCase panics on nil deps — init-time bug.
// seasons is optional (nil → Sonarr seasons are used unchanged).
func NewInstanceMetadataUseCase(lookup InstanceLookup, cache *admininfra.MetadataCache, clock func() time.Time) *InstanceMetadataUseCase {
	if lookup == nil {
		panic("auth.NewInstanceMetadataUseCase: lookup must not be nil")
	}
	if cache == nil {
		panic("auth.NewInstanceMetadataUseCase: cache must not be nil")
	}
	if clock == nil {
		clock = time.Now
	}
	return &InstanceMetadataUseCase{lookup: lookup, cache: cache, clock: clock}
}

// WithSeasonsResolver installs the optional TMDB-first seasons resolver.
// Returns the receiver so wiring can chain. Calling with nil clears it.
func (uc *InstanceMetadataUseCase) WithSeasonsResolver(r SeasonsResolver) *InstanceMetadataUseCase {
	uc.seasons = r
	return uc
}

// WithRadarrLookup installs the optional radarr QP/RF fallback. Sonarr
// resolution stays FIRST and byte-identical; radarr is consulted only when the
// sonarr registry misses the name. Returns the receiver so wiring can chain.
// Ф6-R-6b Gap 2c.
func (uc *InstanceMetadataUseCase) WithRadarrLookup(r RadarrQPRFLookup) *InstanceMetadataUseCase {
	uc.radarr = r
	return uc
}

// resolveQPRF resolves an instance name to its QP/RF client. Sonarr is tried
// FIRST (unchanged behavior); a radarr instance resolves only on a sonarr
// miss when the radarr fallback is wired. Ф6-R-6b Gap 2c.
func (uc *InstanceMetadataUseCase) resolveQPRF(instanceName string) (QPRFClient, bool) {
	if c, ok := uc.lookup.Lookup(instanceName); ok {
		return c, true // ports.SonarrClient satisfies QPRFClient
	}
	if uc.radarr != nil {
		if c, ok := uc.radarr.Lookup(instanceName); ok {
			return c, true
		}
	}
	return nil, false
}

// GetQualityProfiles returns the cached list on hit, else fetches from
// Sonarr and caches the result. Sonarr error + miss → SonarrUnreachable.
// Sonarr error + hit never reaches Sonarr — the cache serves the entry
// for the rest of the TTL window (graceful degradation).
func (uc *InstanceMetadataUseCase) GetQualityProfiles(ctx context.Context, instanceName string) (QualityProfilesResult, error) {
	client, ok := uc.resolveQPRF(instanceName)
	if !ok {
		return QualityProfilesResult{}, instanceNotFound(instanceName)
	}
	if cached, hit := uc.cache.GetQualityProfiles(instanceName); hit {
		return QualityProfilesResult{
			Items: cached, RefreshedAt: uc.clock(),
			CacheStatus: CacheStatusHit, InstanceName: instanceName,
		}, nil
	}
	items, err := client.ListQualityProfiles(ctx)
	if err != nil {
		return QualityProfilesResult{}, sonarrUnreachable(instanceName, err)
	}
	uc.cache.SetQualityProfiles(instanceName, items)
	return QualityProfilesResult{
		Items: items, RefreshedAt: uc.clock(),
		CacheStatus: CacheStatusMiss, InstanceName: instanceName,
	}, nil
}

// GetRootFolders mirrors GetQualityProfiles for /api/v3/rootfolder.
func (uc *InstanceMetadataUseCase) GetRootFolders(ctx context.Context, instanceName string) (RootFoldersResult, error) {
	client, ok := uc.resolveQPRF(instanceName)
	if !ok {
		return RootFoldersResult{}, instanceNotFound(instanceName)
	}
	if cached, hit := uc.cache.GetRootFolders(instanceName); hit {
		return RootFoldersResult{
			Items: cached, RefreshedAt: uc.clock(),
			CacheStatus: CacheStatusHit, InstanceName: instanceName,
		}, nil
	}
	items, err := client.ListRootFolders(ctx)
	if err != nil {
		return RootFoldersResult{}, sonarrUnreachable(instanceName, err)
	}
	uc.cache.SetRootFolders(instanceName, items)
	return RootFoldersResult{
		Items: items, RefreshedAt: uc.clock(),
		CacheStatus: CacheStatusMiss, InstanceName: instanceName,
	}, nil
}

// LookupSeries proxies Sonarr's GET /api/v3/series/lookup for the
// AddToSonarrModal per-season picker (story 524 N-4). Uncached — the
// preview is fast and FE caches via React Query. tvdbID is the TVDB
// integer identifier; the Sonarr term is built as "tvdb:<id>" for a
// deterministic single-row match. Returns the empty slice for "no
// matches" (handler maps to 404). 5xx/network → sonarr_unreachable.
//
// Story 525 — Sonarr's seasons[*].episode_count is `0` for series whose
// TVDB metadata cache is incomplete on the upstream side. When a
// SeasonsResolver is wired, the use case asks it for the authoritative
// per-season episode_count (catalog seasons → TMDB), and overrides the
// Sonarr-supplied counts. Sonarr still supplies title/year/overview/
// image_url + the `monitored` defaults (those are not in TMDB's wire
// shape). The resolver is best-effort: any error or empty result keeps
// the Sonarr seasons unchanged — never blocks the add-to-sonarr flow.
func (uc *InstanceMetadataUseCase) LookupSeries(ctx context.Context, instanceName string, tvdbID int) (SonarrLookupResult, error) {
	client, ok := uc.lookup.Lookup(instanceName)
	if !ok {
		return SonarrLookupResult{}, instanceNotFound(instanceName)
	}
	items, err := client.LookupSeries(ctx, sonarrTVDBTerm(tvdbID))
	if err != nil {
		return SonarrLookupResult{}, sonarrUnreachable(instanceName, err)
	}
	if uc.seasons != nil && len(items) > 0 {
		uc.overrideSeasons(ctx, items, tvdbID)
	}
	return SonarrLookupResult{Items: items, InstanceName: instanceName}, nil
}

// overrideSeasons asks the SeasonsResolver for the authoritative
// per-season episode_count list and merges it into the Sonarr items
// in-place. The merge preserves Sonarr's `monitored` flag for each
// season (TMDB doesn't carry that). Resolver errors and empty results
// are best-effort — the Sonarr seasons stay in place so the modal still
// renders. The tmdbHint (Sonarr's own tmdb_id, if any) lets the
// resolver skip a FindByTVDB round-trip when the canon already knows
// the TMDB id.
func (uc *InstanceMetadataUseCase) overrideSeasons(ctx context.Context, items []ports.SonarrLookupResult, tvdbID int) {
	first := items[0]
	resolved, err := uc.seasons.ResolveSeasons(ctx, tvdbID, first.TMDBID)
	if err != nil || len(resolved) == 0 {
		return
	}
	monitored := make(map[int]bool, len(first.Seasons))
	for _, s := range first.Seasons {
		monitored[s.SeasonNumber] = s.Monitored
	}
	merged := make([]ports.SeasonInfo, 0, len(resolved))
	for _, s := range resolved {
		mon, hadHint := monitored[s.SeasonNumber]
		if !hadHint {
			// TMDB-only season (Sonarr lookup was empty). Default to
			// Sonarr's selection rule: monitor every non-specials
			// season (season 0 stays unmonitored).
			mon = s.SeasonNumber > 0
		}
		merged = append(merged, ports.SeasonInfo{
			SeasonNumber: s.SeasonNumber,
			EpisodeCount: s.EpisodeCount,
			Monitored:    mon,
		})
	}
	first.Seasons = merged
	items[0] = first
}

// sonarrTVDBTerm renders the Sonarr lookup term for a TVDB id. Kept
// as a helper so tests can assert on the wire form without duplicating
// the format string.
func sonarrTVDBTerm(tvdbID int) string {
	return "tvdb:" + strconv.Itoa(tvdbID)
}

// RefreshMetadata evicts both caches for the named instance. Resolves via the
// QP/RF seam so radarr instances (Ф6-R-6b) can refresh their cached metadata
// too; sonarr resolution is unchanged (tried first).
func (uc *InstanceMetadataUseCase) RefreshMetadata(_ context.Context, instanceName string) error {
	if _, ok := uc.resolveQPRF(instanceName); !ok {
		return instanceNotFound(instanceName)
	}
	uc.cache.InvalidateInstance(instanceName)
	return nil
}

func instanceNotFound(name string) error {
	return errors.Join(
		&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
		ports.ErrNotFound,
	)
}

func sonarrUnreachable(name string, cause error) error {
	return &sharedErrors.SonarrUnreachableError{
		Instance: domain.InstanceName(name),
		Cause:    cause,
	}
}
