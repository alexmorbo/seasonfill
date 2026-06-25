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

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	domenrich "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
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
	Person      dompeople.Person
	Biography   string
	BioLanguage string
	// SyncedAt is the last successful TMDB person enrichment timestamp
	// read from person.EnrichmentSyncedAt. nil = never enriched, which
	// the H-2 rule 1 surfaces as degraded[SourceTMDBPerson].
	SyncedAt       *time.Time
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
//
// Canon carries the canon series row when classifyCredit found
// one but the cache lookup was empty (CategoryCanon). It stays
// zero-valued when the credit has no canon row at all
// (CategoryTMDB) — the wire-DTO mapper consults Canon.ID to
// decide whether to populate dto.OtherCreditEntry.SeriesID.
type OtherCredit struct {
	Credit dompeople.PersonCredit
	Canon  series.Canon
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
		// SyncedAt comes straight from the canon row's
		// people.enrichment_synced_at column (D-3 migration 000014).
		// nil for cold persons; rule 1 in computeDegraded surfaces it
		// as degraded[SourceTMDBPerson] below.
		SyncedAt: person.EnrichmentSyncedAt,
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
		cat, canon, instances := uc.classifyCredit(ctx, pc)
		switch cat {
		case CategoryLibrary:
			libCredits = append(libCredits, LibraryCredit{
				Credit:    pc,
				Canon:     canon,
				Instances: instances,
			})
		case CategoryCanon:
			otherCredits = append(otherCredits, OtherCredit{Credit: pc, Canon: canon})
		default: // CategoryTMDB
			otherCredits = append(otherCredits, OtherCredit{Credit: pc})
		}
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

	degraded := uc.computeDegraded(person)
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

// CreditCategory tells the classifier whether a credit goes to
// library_credits (has canon + cache), other_credits with a
// canon row (TMDB-fallback route available), or other_credits
// without a canon row at all (external TMDB link only).
type CreditCategory int

const (
	// CategoryTMDB — no canon row → FE renders external TMDB link only.
	CategoryTMDB CreditCategory = iota
	// CategoryCanon — canon row, no cache → FE renders internal
	// route, SeriesDetail resolves via TMDBFallbackUseCase.
	CategoryCanon
	// CategoryLibrary — canon row + live cache → primary library route.
	CategoryLibrary
)

// classifyCredit returns (category, canon, instances) for one
// person_credits row. Canon is zero-valued when category is
// CategoryTMDB. Instances is non-empty only when category is
// CategoryLibrary.
func (uc *UseCase) classifyCredit(ctx context.Context, pc dompeople.PersonCredit) (CreditCategory, series.Canon, []LibraryInstance) {
	if pc.MediaType != "tv" {
		return CategoryTMDB, series.Canon{}, nil
	}
	canon, err := uc.d.SeriesByTMDB.GetByTMDBID(ctx, domain.TMDBID(pc.TMDBMediaID))
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if !errors.As(err, &seriesNF) {
			uc.d.Logger.WarnContext(ctx, "person_classify_canon_lookup_failed",
				slog.Int64("tmdb_media_id", pc.TMDBMediaID),
				slog.String("error", err.Error()))
		}
		return CategoryTMDB, series.Canon{}, nil
	}
	caches, err := uc.d.SeriesCache.ListBySeriesID(ctx, canon.ID)
	if err != nil {
		uc.d.Logger.WarnContext(ctx, "person_classify_cache_list_failed",
			slog.Int64("series_id", int64(canon.ID)),
			slog.String("error", err.Error()))
		// We have the canon row but cache lookup failed — fall
		// through to CategoryCanon so the FE still gets an
		// internal-routable card.
		return CategoryCanon, canon, nil
	}
	if len(caches) == 0 {
		return CategoryCanon, canon, nil
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
	return CategoryLibrary, canon, instances
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
//  1. Stub hydration → degraded.
//  2. person.EnrichmentSyncedAt nil → degraded (never enriched).
//
// The pre-D-3 "outcome=error → degraded" branch is dropped here because
// the H-2 use case doesn't pull enrichment_errors rows (the legacy
// SyncLogLookup port is retired); the live-error surface is in the
// composer's degraded[] path, not on /api/v1/people/:tmdbId. If
// per-person error rendering is required later, plumb
// EnrichmentErrorRepo through the H-2 deps.
func (uc *UseCase) computeDegraded(p dompeople.Person) []domenrich.Source {
	if p.Hydration == dompeople.HydrationStub {
		return []domenrich.Source{domenrich.SourceTMDBPerson}
	}
	if p.EnrichmentSyncedAt == nil {
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
