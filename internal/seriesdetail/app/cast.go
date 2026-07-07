// Package seriesdetail — H-1 cast & crew composer
// (Story 216 / PRD §5.7 row "/series/:id/cast"). Sibling to
// composer.go but never runs the 9-branch errgroup: the cast/crew
// payload is a single-purpose read, not the composite series
// document.
package seriesdetail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// CastPage is the composer's domain object for the H-1 page —
// the handler maps it onto dto.SeriesCastResponse without further
// DB queries.
type CastPage struct {
	Instance          domain.InstanceName
	SonarrSeriesID    domain.SonarrSeriesID
	SeriesID          domain.SeriesID
	Lang              string
	Summary           SeriesSummary
	TotalEpisodeCount int
	Cast              []CastEntry
	Crew              []CrewEntry
	SyncedAt          time.Time
	// ServedLanguage is the BCP-47 language the hero summary title (the cast
	// page's principal localized text) was served in (W15-9). Empty when the
	// title fell through to canon.OriginalTitle. The handler appends
	// "missing_lang" when it differs from the requested Lang.
	ServedLanguage string
}

// SeriesSummary is the lightweight series-meta projection the cast
// page hero consumes. Carries enough for title + poster + status
// pill + year range without a second round-trip to the series
// detail endpoint.
type SeriesSummary struct {
	Title          string
	PosterAsset    *string
	Status         string
	FirstAiredYear *int
	LastAiredYear  *int
}

// CastEntry is one cast row with the person + credit + in_library
// flag resolved.
type CastEntry struct {
	Credit    people.SeriesCredit
	Person    people.Person
	InLibrary bool
}

// CrewEntry is one crew row. Same shape as CastEntry — the
// person + credit + flag separation keeps the handler mapping
// trivial.
type CrewEntry struct {
	Credit    people.SeriesCredit
	Person    people.Person
	InLibrary bool
}

// CastDeps groups the cast composer's dependencies. Subset of the
// 215 Deps struct — the H-1 read needs only series_cache resolution,
// series_people, people, person_credits (for in_library), episodes
// count (for total), and series_cache reverse lookup (also for
// in_library).
type CastDeps struct {
	SeriesCache       SeriesCachePort
	SeriesCacheLookup SeriesCacheLookupPort
	Series            SeriesPort
	// SeriesTexts / SeriesMediaTexts (S-E3a) — resolve the hero title +
	// poster from the i18n side-tables (lang → en-US; title falls back to
	// canon OriginalTitle). Canon no longer carries title/poster_asset.
	// nil-OK: title degrades to OriginalTitle, poster to nil (monogram).
	SeriesTexts      SeriesTextsPort
	SeriesMediaTexts SeriesMediaTextsPort
	SeriesPeople     SeriesPeoplePort
	People           PeoplePort
	PersonCredits    PersonCreditsPort
	EpisodesCount    EpisodesCountPort
	Logger           *slog.Logger
	Now              func() time.Time
	// MediaResolver (story 312) — translates raw TMDB ProfileAsset / PosterAsset
	// paths into sha256 hashes the frontend serves via /api/v1/media/:hash. Nil
	// is allowed at wiring; NewCastComposer defaults to a no-op resolver.
	MediaResolver *media.Resolver
}

// CastComposer is the one application use case for the H-1 page.
type CastComposer struct {
	d CastDeps
}

// NewCastComposer constructs the composer. Logger defaults to
// slog.Default; Now defaults to time.Now.UTC.
func NewCastComposer(d CastDeps) *CastComposer {
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MediaResolver == nil {
		d.MediaResolver = media.NewNopResolver()
	}
	return &CastComposer{d: d}
}

// Get returns the full cast & crew payload for the
// (instance, sonarr_series_id) pair. `lang` defaults to "en-US"
// when empty — currently only echoed on the response (cast list
// has no per-language fields in v1); reserved for H-2 parity.
func (c *CastComposer) Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID, lang string) (*CastPage, error) {
	lang = resolveLang(lang)
	start := c.d.Now()

	// Step 1 — series_cache → series_id. Same invariant as 215.
	cache, err := c.d.SeriesCache.Get(ctx, instanceName, sonarrSeriesID)
	if err != nil {
		return nil, fmt.Errorf("series_cache lookup: %w", err)
	}
	if cache.SeriesID == nil || *cache.SeriesID == 0 {
		// Preserve typed chain so middleware dispatches series_cache_not_found.
		return nil, errors.Join(
			&sharedErrors.SeriesCacheNotFoundError{
				InstanceName:   instanceName,
				SonarrSeriesID: sonarrSeriesID,
			},
			ports.ErrNotFound,
		)
	}
	seriesID := *cache.SeriesID

	// Step 2 — load canon row. Missing canon → 404. Row drives the
	// hero summary so the cast page renders title + poster + status
	// + year range without a second round-trip to the series-detail
	// endpoint (story 303).
	canon, gerr := c.d.Series.Get(ctx, seriesID)
	if gerr != nil {
		return nil, fmt.Errorf("series canon load: %w", gerr)
	}

	// S-E3a — hero title from series_texts (lang → en-US), else canon
	// OriginalTitle; hero poster raw path from series_media_texts. Canon
	// carries neither after S-E3a.
	heroTitle := ""
	servedLang := ""
	if c.d.SeriesTexts != nil {
		if t, terr := c.d.SeriesTexts.GetWithFallback(ctx, seriesID, lang); terr == nil && t.Title != nil && *t.Title != "" {
			heroTitle = *t.Title
			// W15-9 — the summary title's row language is the served signal;
			// only meaningful when the title came from series_texts (not the
			// OriginalTitle fallback below).
			servedLang = t.Language
		}
	}
	if heroTitle == "" && canon.OriginalTitle != nil {
		heroTitle = *canon.OriginalTitle
	}
	var posterRaw *string
	if c.d.SeriesMediaTexts != nil {
		if mt, merr := c.d.SeriesMediaTexts.GetWithFallback(ctx, seriesID, lang); merr == nil && mt.PosterAsset != nil && *mt.PosterAsset != "" {
			p := *mt.PosterAsset
			posterRaw = &p
		}
	}

	out := &CastPage{
		Instance:       instanceName,
		SonarrSeriesID: sonarrSeriesID,
		SeriesID:       seriesID,
		Lang:           lang,
		Summary:        buildSeriesSummary(canon, heroTitle, posterRaw),
		ServedLanguage: servedLang,
	}

	// Story 312 + 316: hero summary poster — sync first-fold fetch with a
	// 1.5s per-asset budget. Cast/crew profiles stay on async-only Resolve.
	{
		syncCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		out.Summary.PosterAsset = c.d.MediaResolver.ResolveSync(syncCtx, out.Summary.PosterAsset, "w342", "poster_w342")
		cancel()
	}

	// Step 3 — total_episode_count. One indexed count query.
	// Failure is non-fatal: zero is a valid value (cold series,
	// no episode hydration). Logged so it shows up in obs.
	total, cerr := c.d.EpisodesCount.CountBySeries(ctx, seriesID)
	if cerr != nil {
		c.d.Logger.WarnContext(ctx, "cast_total_episode_count_failed",
			slog.String("instance_name", string(instanceName)),
			slog.Int64("series_id", int64(seriesID)),
			slog.String("error", cerr.Error()))
		total = 0
	}
	out.TotalEpisodeCount = total

	// Step 4 — load cast (kind='cast'). Repository orders by
	// (kind ASC, credit_order ASC NULLS LAST) — exactly the
	// shape this list wants.
	castCredits, err := c.d.SeriesPeople.ListBySeries(ctx, seriesID, people.SeriesCreditCast, lang)
	if err != nil {
		return nil, fmt.Errorf("list cast: %w", err)
	}

	// Step 5 — load crew (kind='crew'). Same repository call.
	// Default ordering is (kind ASC, credit_order ASC) — wrong
	// shape for crew; we re-sort by (department, name) post-join
	// below.
	crewCredits, err := c.d.SeriesPeople.ListBySeries(ctx, seriesID, people.SeriesCreditCrew, lang)
	if err != nil {
		return nil, fmt.Errorf("list crew: %w", err)
	}

	// Step 6 — batch fetch people rows in one call. Dedupe by
	// PersonID so the same person on cast + crew (or multiple crew
	// jobs) issues one people row fetch.
	personIDs := make([]int64, 0, len(castCredits)+len(crewCredits))
	seen := map[int64]bool{}
	for _, cr := range castCredits {
		if !seen[cr.PersonID] {
			personIDs = append(personIDs, cr.PersonID)
			seen[cr.PersonID] = true
		}
	}
	for _, cr := range crewCredits {
		if !seen[cr.PersonID] {
			personIDs = append(personIDs, cr.PersonID)
			seen[cr.PersonID] = true
		}
	}

	personByID := map[int64]people.Person{}
	if len(personIDs) > 0 {
		persons, perr := c.d.People.ListByIDsWithNameFallback(ctx, personIDs, lang)
		if perr != nil {
			return nil, fmt.Errorf("list people: %w", perr)
		}
		for _, p := range persons {
			personByID[p.ID] = p
		}
	}

	// Step 7 — batch in_library probe (Story 556 / E-1 Z7). Collapses the
	// per-person N+1 (each probeInLibrary fired
	//   1 ListByPerson + N GetByTMDBID + M ListBySeriesID
	// queries) into:
	//   1 ListByPerson per unique person (unchanged — indexed, bounded)
	//   1 Series.ListByTMDBIDs across ALL cast/crew credits
	//   1 SeriesCacheLookup.ListBySeriesIDs across all resolved series ids
	// For series 31 (Rick & Morty, ~235 persons) this drops ~800 statements
	// to 2.
	inLibraryCache := make(map[int64]bool, len(personIDs))
	if len(personIDs) > 0 {
		// Pass 1: per-person credits (one ListByPerson query per unique
		// person — these stay since person_credits is keyed on person_id
		// and the batch alternative would be a wider IN(?) scan with
		// looser locality). Collect every TMDB media id that has
		// media_type='tv' AND tmdb_media_id != 0 — those are the
		// candidates we need canon rows for.
		tmdbIDSet := make(map[domain.TMDBID]struct{})
		creditsByPerson := make(map[int64][]PersonCreditRef, len(personIDs))
		for _, pid := range personIDs {
			credits, perr := c.d.PersonCredits.ListByPerson(ctx, pid)
			if perr != nil {
				if !errors.Is(perr, ports.ErrNotFound) {
					c.d.Logger.WarnContext(ctx, "cast_in_library_probe_failed",
						slog.Int64("person_id", pid),
						slog.Int64("series_id", int64(seriesID)),
						slog.String("code", sharedErrors.ErrorCode(perr)),
						slog.String("error", perr.Error()))
				}
				continue
			}
			creditsByPerson[pid] = credits
			for _, pc := range credits {
				if pc.MediaType != "tv" {
					continue
				}
				if pc.TMDBMediaID == 0 {
					continue
				}
				tmdbIDSet[domain.TMDBID(pc.TMDBMediaID)] = struct{}{}
			}
		}

		// Pass 2: one Series.ListByTMDBIDs across every collected id.
		// Build a tmdbID → seriesID map for the in-loop lookup.
		tmdbIDs := make([]domain.TMDBID, 0, len(tmdbIDSet))
		for id := range tmdbIDSet {
			tmdbIDs = append(tmdbIDs, id)
		}
		seriesByTMDB := make(map[domain.TMDBID]domain.SeriesID, len(tmdbIDs))
		if len(tmdbIDs) > 0 {
			canons, lerr := c.d.Series.ListByTMDBIDs(ctx, tmdbIDs)
			if lerr != nil {
				c.d.Logger.WarnContext(ctx, "cast_in_library_series_batch_failed",
					slog.Int("tmdb_ids", len(tmdbIDs)),
					slog.String("error", lerr.Error()))
				// Degraded: no canon mapping — every person stays false.
				// Continue so the response still ships (matches the
				// per-person warn-and-skip posture).
			}
			for _, canon := range canons {
				if canon.TMDBID == nil || *canon.TMDBID == 0 {
					continue
				}
				seriesByTMDB[*canon.TMDBID] = canon.ID
			}
		}

		// Pass 3: one SeriesCacheLookup.ListBySeriesIDs across every
		// resolved series id (minus self — preserve the self-link
		// suppression invariant from the legacy probeInLibrary).
		cacheNeeded := make(map[domain.SeriesID]struct{}, len(seriesByTMDB))
		for _, sid := range seriesByTMDB {
			if sid == seriesID {
				continue
			}
			cacheNeeded[sid] = struct{}{}
		}
		seriesIDs := make([]domain.SeriesID, 0, len(cacheNeeded))
		for sid := range cacheNeeded {
			seriesIDs = append(seriesIDs, sid)
		}
		var cachesBySeriesID map[domain.SeriesID][]series.CacheEntry
		if len(seriesIDs) > 0 {
			var lerr error
			cachesBySeriesID, lerr = c.d.SeriesCacheLookup.ListBySeriesIDs(ctx, seriesIDs)
			if lerr != nil {
				c.d.Logger.WarnContext(ctx, "cast_in_library_cache_batch_failed",
					slog.Int("series_ids", len(seriesIDs)),
					slog.String("error", lerr.Error()))
				cachesBySeriesID = nil
			}
		}

		// Pass 4: O(1) probe per person.
		for _, pid := range personIDs {
			credits, ok := creditsByPerson[pid]
			if !ok {
				inLibraryCache[pid] = false
				continue
			}
			inLibraryCache[pid] = personInLibrary(credits, seriesID, seriesByTMDB, cachesBySeriesID)
		}
	}

	// Step 8 — assemble cast entries. ListBySeries already orders
	// by credit_order ASC — preserve order.
	out.Cast = make([]CastEntry, 0, len(castCredits))
	for _, cr := range castCredits {
		p, ok := personByID[cr.PersonID]
		if !ok {
			// Person row missing — credit references an unhydrated
			// stub. Skip (cast list shrinks gracefully — matches
			// G-1 pattern).
			continue
		}
		out.Cast = append(out.Cast, CastEntry{
			Credit:    cr,
			Person:    p,
			InLibrary: inLibraryCache[cr.PersonID],
		})
	}

	// Step 9 — assemble crew entries, then sort by
	// (department ASC, name ASC). Duplicates with distinct jobs
	// are preserved (PRD §5.3 row "series_people"); frontend
	// dedupes visually.
	crew := make([]CrewEntry, 0, len(crewCredits))
	for _, cr := range crewCredits {
		p, ok := personByID[cr.PersonID]
		if !ok {
			continue
		}
		crew = append(crew, CrewEntry{
			Credit:    cr,
			Person:    p,
			InLibrary: inLibraryCache[cr.PersonID],
		})
	}
	sort.SliceStable(crew, func(i, j int) bool {
		di := derefStr(crew[i].Credit.Department)
		dj := derefStr(crew[j].Credit.Department)
		if di != dj {
			return di < dj
		}
		// Same department → sort by name; within (department,
		// name) preserve repository order so distinct jobs render
		// in TMDB credit_order.
		if crew[i].Person.Name != crew[j].Person.Name {
			return crew[i].Person.Name < crew[j].Person.Name
		}
		// Last fallback: job string. Keeps duplicate-person
		// ordering deterministic across runs.
		return derefStr(crew[i].Credit.Job) < derefStr(crew[j].Credit.Job)
	})
	out.Crew = crew

	// Story 312: cast + crew profile assets.
	for i := range out.Cast {
		out.Cast[i].Person.ProfileAsset = c.d.MediaResolver.Resolve(ctx, out.Cast[i].Person.ProfileAsset, "w185", "profile_w185")
	}
	for i := range out.Crew {
		out.Crew[i].Person.ProfileAsset = c.d.MediaResolver.Resolve(ctx, out.Crew[i].Person.ProfileAsset, "w185", "profile_w185")
	}

	out.SyncedAt = c.d.Now()

	c.d.Logger.InfoContext(ctx, "series_cast_composed",
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrSeriesID)),
		slog.Int64("series_id", int64(seriesID)),
		slog.Int("cast_count", len(out.Cast)),
		slog.Int("crew_count", len(out.Crew)),
		slog.Int("total_episode_count", out.TotalEpisodeCount),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	return out, nil
}

// personInLibrary collapses the per-credit probe into O(1) map
// lookups. Matches the legacy probeInLibrary semantics exactly:
// any TV credit whose canon series.id is in the operator's library
// (and is NOT the current series) flips the flag.
//
// Story 556 (E-1 Z7) — replaces the per-person probeInLibrary +
// resolveSeriesByTMDB helpers; production callers go through the
// 4-pass batch in CastComposer.Get.
func personInLibrary(
	credits []PersonCreditRef,
	currentSeriesID domain.SeriesID,
	seriesByTMDB map[domain.TMDBID]domain.SeriesID,
	cachesBySeriesID map[domain.SeriesID][]series.CacheEntry,
) bool {
	for _, pc := range credits {
		if pc.MediaType != "tv" {
			continue
		}
		if pc.TMDBMediaID == 0 {
			continue
		}
		sid, ok := seriesByTMDB[domain.TMDBID(pc.TMDBMediaID)]
		if !ok {
			continue
		}
		if sid == currentSeriesID {
			continue
		}
		if len(cachesBySeriesID[sid]) > 0 {
			return true
		}
	}
	return false
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// buildSeriesSummary projects a canon row onto the cast-page hero
// shape. Mirrors handlers.mapStatusPill for the status token and
// handlers.mapHero for the year extraction — kept composer-side
// so the DTO mapping stays a pure projection.
// S-E3a — title + posterRaw are staged upstream (series_texts /
// series_media_texts → en-US; title falls back to canon OriginalTitle) since
// canon no longer carries them.
func buildSeriesSummary(c series.Canon, title string, posterRaw *string) SeriesSummary {
	s := SeriesSummary{
		Title:       title,
		PosterAsset: posterRaw,
		Status:      mapStatusToken(c.Status, c.InProduction),
	}
	if c.Year != nil {
		ys := *c.Year
		s.FirstAiredYear = &ys
	} else if c.FirstAirDate != nil {
		// Heal TMDB-only rows whose year column was never derived — display
		// derive from first_air_date (writes nothing). Mirrors LastAiredYear.
		ys := c.FirstAirDate.Year()
		s.FirstAiredYear = &ys
	}
	if c.LastAirDate != nil {
		ye := c.LastAirDate.Year()
		s.LastAiredYear = &ye
	}
	return s
}

// mapStatusToken normalises upstream status strings + InProduction
// onto the design-brief's status token set
// (continuing / ended / canceled / in_production / upcoming /
// unknown). Identical mapping to handlers.mapStatusPill — kept here
// so the cast composer doesn't depend on the HTTP layer.
func mapStatusToken(status *string, inProduction bool) string {
	raw := ""
	if status != nil {
		raw = strings.ToLower(strings.TrimSpace(*status))
	}
	switch {
	case strings.Contains(raw, "cancel"):
		return "canceled"
	case strings.Contains(raw, "ended"):
		return "ended"
	case strings.Contains(raw, "upcoming") || strings.Contains(raw, "planned"):
		return "upcoming"
	case strings.Contains(raw, "production") && !strings.Contains(raw, "post"):
		return "in_production"
	case strings.Contains(raw, "continu") || strings.Contains(raw, "ongoing") || strings.Contains(raw, "returning"):
		return "continuing"
	case inProduction:
		return "in_production"
	case raw == "":
		return "unknown"
	}
	return "unknown"
}
