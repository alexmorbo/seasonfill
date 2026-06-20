// Package people — H-2 person detail use case (Story 217 / PRD
// v4 §5.7 row "/people/:tmdbId"). Resolves a TMDB person id to
// the canon row + biography + library/other credit classification
// + sync line + degraded flag. Stub persons trigger a
// PriorityHot dispatcher enqueue but return 200 immediately
// (NEVER 202 — design-handoff explicit).
package people

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	appenrich "github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	domenrich "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// SortKey controls library_credits[] ordering. recent (default) =
// series.last_aired_at DESC NULLS LAST; episodes =
// person_credits.episode_count DESC NULLS LAST; title =
// series.title ASC. Empty / unknown defaults to SortRecent.
type SortKey string

const (
	SortRecent   SortKey = "recent"
	SortEpisodes SortKey = "episodes"
	SortTitle    SortKey = "title"
)

// IsValid reports whether s is one of the three known sort keys.
func (s SortKey) IsValid() bool {
	return s == SortRecent || s == SortEpisodes || s == SortTitle
}

// PersonDetail is the domain object the H-2 use case returns.
// The handler maps it onto dto.PersonDetailResponse without
// touching the database again.
type PersonDetail struct {
	Person         dompeople.Person
	Biography      string
	BioLanguage    string
	Sync           *domenrich.SyncLog
	LibraryCredits []LibraryCredit
	OtherCredits   []OtherCredit
	Degraded       []domenrich.Source
}

// LibraryCredit is one library_credit — person_credits row joined
// to a canon series with at least one live series_cache reference.
type LibraryCredit struct {
	Credit    dompeople.PersonCredit
	Canon     series.Canon
	Instances []LibraryInstance
}

// LibraryInstance is one row of `LibraryCredit.Instances`: the
// Sonarr instance name plus the per-instance Sonarr series id. The
// frontend uses (InstanceName, SonarrSeriesID) to deep-link into
// the Series Detail route — the canon `series.id` is NOT a valid
// URL parameter there (the route loads
// `/api/v1/instances/:name/series/:sonarrId` which keys on the
// per-instance integer, not the canon row id).
type LibraryInstance struct {
	InstanceName   domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
}

// OtherCredit is one TMDB-only credit — either the canon series
// row doesn't exist OR it has no live series_cache references
// (e.g., recommendation stubs from 215's branch).
type OtherCredit struct {
	Credit dompeople.PersonCredit
}

// Deps groups the use case's dependencies. Every port is required;
// the dispatcher (Enqueuer) MAY be nil in test fixtures that
// don't care about stub-on-demand. MediaResolver MAY be nil — the
// use case substitutes a no-op resolver that leaves every *_asset
// field as the raw TMDB path (legacy behavior; broken in prod —
// production wiring MUST pass the real resolver).
type Deps struct {
	People        PeopleReader
	PersonCredits PersonCreditsReader
	SeriesByTMDB  SeriesByTMDBLookup
	SeriesCache   SeriesCacheLookup
	SyncLog       SyncLogLookup
	Enqueuer      PersonEnqueuer
	MediaResolver MediaResolver
	Logger        *slog.Logger
	Now           func() time.Time
}

// UseCase is the H-2 application use case.
type UseCase struct {
	d Deps
}

// NewUseCase constructs the H-2 use case. Logger defaults to
// slog.Default; Now defaults to time.Now.UTC.
func NewUseCase(d Deps) *UseCase {
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MediaResolver == nil {
		d.MediaResolver = nopMediaResolver{}
	}
	return &UseCase{d: d}
}

// nopMediaResolver is the zero MediaResolver — every Resolve call
// returns nil, so wire fields stay nil and the frontend renders a
// monogram placeholder. Used when the use case is constructed without
// the media subsystem wired (tests, boot-time fallback).
type nopMediaResolver struct{}

func (nopMediaResolver) Resolve(_ context.Context, _ *string, _, _ string) *string     { return nil }
func (nopMediaResolver) ResolveSync(_ context.Context, _ *string, _, _ string) *string { return nil }

// Get runs the H-2 workflow for (tmdbID, lang, sort).
func (uc *UseCase) Get(ctx context.Context, tmdbID domain.TMDBID, lang string, sortKey string) (*PersonDetail, error) {
	if tmdbID <= 0 {
		uc.d.Logger.WarnContext(ctx, "person_invalid_tmdb_id",
			slog.Int("tmdb_person_id", int(tmdbID)),
			slog.String("code", "tmdb_not_found"))
		// Carry the typed err so middleware can dispatch on
		// TMDBNotFoundError; join with ports.ErrNotFound so legacy
		// errors.Is(err, ports.ErrNotFound) callers keep working.
		return nil, errors.Join(
			&sharedErrors.TMDBNotFoundError{ID: int(tmdbID)},
			ports.ErrNotFound,
		)
	}
	lang = normalizeLang(lang)
	sk := resolveSort(sortKey)
	start := uc.d.Now()

	bare, err := uc.d.People.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		return nil, fmt.Errorf("people get by tmdb_id: %w", err)
	}

	person, err := uc.d.People.GetWithBio(ctx, bare.ID, lang)
	if err != nil {
		return nil, fmt.Errorf("people get with bio: %w", err)
	}

	out := &PersonDetail{
		Person:      person,
		Biography:   person.Biography,
		BioLanguage: person.BiographyLanguage,
	}

	syncLog, sErr := uc.d.SyncLog.GetLastSync(ctx,
		domenrich.EntityTypePerson, person.ID, domenrich.SourceTMDBPerson)
	switch {
	case sErr == nil:
		row := syncLog
		out.Sync = &row
	case errors.Is(sErr, ports.ErrNotFound):
		// Expected for cold persons. Leave Sync nil; degraded will
		// fire rule 1 when this surfaces below.
	default:
		uc.d.Logger.WarnContext(ctx, "person_sync_log_lookup_failed",
			slog.Int("tmdb_person_id", int(tmdbID)),
			slog.Int64("person_id", person.ID),
			slog.String("error", sErr.Error()))
	}

	credits, cErr := uc.d.PersonCredits.ListByPerson(ctx, person.ID)
	if cErr != nil {
		uc.d.Logger.WarnContext(ctx, "person_credits_list_failed",
			slog.Int("tmdb_person_id", int(tmdbID)),
			slog.Int64("person_id", person.ID),
			slog.String("error", cErr.Error()))
		credits = nil
	}

	libCredits := make([]LibraryCredit, 0, len(credits)/2)
	otherCredits := make([]OtherCredit, 0, len(credits))
	for _, pc := range credits {
		isLib, canon, instances := uc.classifyCredit(ctx, pc)
		if isLib {
			libCredits = append(libCredits, LibraryCredit{
				Credit:    pc,
				Canon:     canon,
				Instances: instances,
			})
			continue
		}
		otherCredits = append(otherCredits, OtherCredit{Credit: pc})
	}

	sortLibraryCredits(libCredits, sk)

	out.LibraryCredits = libCredits
	out.OtherCredits = otherCredits

	// Story 315 — resolve TMDB raw paths to sha256 hashes the
	// frontend can serve via /api/v1/media/:hash. Misses → nil →
	// monogram fallback. Mirrors seriesdetail.Composer.resolveAssets.
	// Story 316 — hero portrait gets a 1.5s sync fetch budget; library
	// credit posters stay async-only.
	{
		syncCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		out.Person.ProfileAsset = uc.d.MediaResolver.ResolveSync(syncCtx, out.Person.ProfileAsset, "w185", "profile_w185")
		cancel()
	}
	for i := range out.LibraryCredits {
		out.LibraryCredits[i].Canon.PosterAsset = uc.d.MediaResolver.Resolve(ctx, out.LibraryCredits[i].Canon.PosterAsset, "w342", "poster_w342")
	}
	// Story 317 — OtherCredits posters. Movies + canon-less TV stubs. Same
	// resolver, w185 (the frontend's grid is denser → smaller variant). The
	// async miss-enqueue makes the bytes available on subsequent loads.
	for i := range out.OtherCredits {
		out.OtherCredits[i].Credit.PosterAsset = uc.d.MediaResolver.Resolve(ctx, out.OtherCredits[i].Credit.PosterAsset, "w185", "poster_w185")
	}

	degraded := uc.computeDegraded(person, out.Sync)
	out.Degraded = degraded
	if person.Hydration == dompeople.HydrationStub {
		if uc.d.Enqueuer != nil {
			uc.d.Enqueuer.Enqueue(appenrich.EntityPerson, person.ID, appenrich.PriorityHot)
		} else {
			uc.d.Logger.WarnContext(ctx, "person_stub_enqueue_skipped_nil_enqueuer",
				slog.Int("tmdb_person_id", int(tmdbID)),
				slog.Int64("person_id", person.ID))
		}
	}

	uc.d.Logger.InfoContext(ctx, "person_detail_composed",
		slog.Int("tmdb_person_id", int(tmdbID)),
		slog.Int64("person_id", person.ID),
		slog.String("lang", lang),
		slog.String("sort", string(sk)),
		slog.String("hydration", string(person.Hydration)),
		slog.Int("library_count", len(libCredits)),
		slog.Int("other_count", len(otherCredits)),
		slog.Int("degraded_count", len(degraded)),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	return out, nil
}

// classifyCredit returns (isLibrary, canon, instances) for one
// person_credits row.
func (uc *UseCase) classifyCredit(ctx context.Context, pc dompeople.PersonCredit) (bool, series.Canon, []LibraryInstance) {
	if pc.MediaType != "tv" {
		return false, series.Canon{}, nil
	}
	canon, err := uc.d.SeriesByTMDB.GetByTMDBID(ctx, domain.TMDBID(pc.TMDBMediaID))
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if !errors.As(err, &seriesNF) {
			uc.d.Logger.WarnContext(ctx, "person_classify_canon_lookup_failed",
				slog.Int64("tmdb_media_id", pc.TMDBMediaID),
				slog.String("error", err.Error()))
		}
		return false, series.Canon{}, nil
	}
	caches, err := uc.d.SeriesCache.ListBySeriesID(ctx, canon.ID)
	if err != nil {
		uc.d.Logger.WarnContext(ctx, "person_classify_cache_list_failed",
			slog.Int64("series_id", int64(canon.ID)),
			slog.String("error", err.Error()))
		return false, series.Canon{}, nil
	}
	if len(caches) == 0 {
		return false, series.Canon{}, nil
	}
	instances := make([]LibraryInstance, 0, len(caches))
	seen := map[domain.InstanceName]bool{}
	for _, ce := range caches {
		if seen[ce.InstanceName] {
			// First cache row per instance wins. Duplicate rows
			// happen when a series is re-added to the same Sonarr
			// instance under a different sonarr_series_id (rare —
			// only after the operator deleted+re-added). We pick
			// the first row deterministically because
			// SeriesCacheRepository.ListBySeriesID returns rows
			// ordered by (instance_name ASC, sonarr_series_id ASC)
			// and the user-facing link must remain stable across
			// page loads.
			continue
		}
		seen[ce.InstanceName] = true
		instances = append(instances, LibraryInstance{
			InstanceName:   ce.InstanceName,
			SonarrSeriesID: ce.SonarrSeriesID,
		})
	}
	sort.SliceStable(instances, func(i, j int) bool {
		return instances[i].InstanceName < instances[j].InstanceName
	})
	return true, canon, instances
}

// sortLibraryCredits applies the per-sort-key ordering to the
// in-memory library_credits slice. All sorts are stable so the
// repository's underlying ordering breaks ties deterministically.
func sortLibraryCredits(libs []LibraryCredit, sk SortKey) {
	switch sk {
	case SortEpisodes:
		sort.SliceStable(libs, func(i, j int) bool {
			niI := libs[i].Credit.EpisodeCount == nil
			niJ := libs[j].Credit.EpisodeCount == nil
			if niI != niJ {
				return !niI
			}
			if niI && niJ {
				return false
			}
			return *libs[i].Credit.EpisodeCount > *libs[j].Credit.EpisodeCount
		})
	case SortTitle:
		sort.SliceStable(libs, func(i, j int) bool {
			return strings.ToLower(libs[i].Canon.Title) < strings.ToLower(libs[j].Canon.Title)
		})
	default: // SortRecent
		sort.SliceStable(libs, func(i, j int) bool {
			ai, aj := libs[i].Canon.LastAirDate, libs[j].Canon.LastAirDate
			niI := ai == nil
			niJ := aj == nil
			if niI != niJ {
				return !niI
			}
			if niI && niJ {
				return false
			}
			return ai.After(*aj)
		})
	}
}

// computeDegraded walks the H-2 degraded rules for the
// tmdb_person source only:
//  1. No sync_log row → degraded.
//  2. sync_log.outcome == error → degraded.
//  3. Stub hydration → degraded.
func (uc *UseCase) computeDegraded(p dompeople.Person, log *domenrich.SyncLog) []domenrich.Source {
	if p.Hydration == dompeople.HydrationStub {
		return []domenrich.Source{domenrich.SourceTMDBPerson}
	}
	if log == nil {
		return []domenrich.Source{domenrich.SourceTMDBPerson}
	}
	if log.Outcome == domenrich.OutcomeError {
		return []domenrich.Source{domenrich.SourceTMDBPerson}
	}
	return nil
}

// normalizeLang trims + caps at 35 chars + defaults to en-US.
func normalizeLang(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" || len(lang) > 35 {
		return "en-US"
	}
	return lang
}

// resolveSort maps the raw query string to a typed SortKey. Empty
// or unknown defaults to SortRecent.
func resolveSort(raw string) SortKey {
	k := SortKey(strings.TrimSpace(raw))
	if k.IsValid() {
		return k
	}
	return SortRecent
}
