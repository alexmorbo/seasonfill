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

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
)

// CastPage is the composer's domain object for the H-1 page —
// the handler maps it onto dto.SeriesCastResponse without further
// DB queries.
type CastPage struct {
	Instance          string
	SonarrSeriesID    int
	SeriesID          int64
	Lang              string
	Summary           SeriesSummary
	TotalEpisodeCount int
	Cast              []CastEntry
	Crew              []CrewEntry
	SyncedAt          time.Time
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
	SeriesPeople      SeriesPeoplePort
	People            PeoplePort
	PersonCredits     PersonCreditsPort
	EpisodesCount     EpisodesCountPort
	Logger            *slog.Logger
	Now               func() time.Time
}

// CastComposer is the one application use case for the H-1 page.
type CastComposer struct {
	d CastDeps
}

// NewCastComposer constructs the composer. Logger defaults to
// slog.Default; Now defaults to time.Now.UTC.
func NewCastComposer(d CastDeps) *CastComposer {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &CastComposer{d: d}
}

// Get returns the full cast & crew payload for the
// (instance, sonarr_series_id) pair. `lang` defaults to "en-US"
// when empty — currently only echoed on the response (cast list
// has no per-language fields in v1); reserved for H-2 parity.
func (c *CastComposer) Get(ctx context.Context, instanceName string, sonarrSeriesID int, lang string) (*CastPage, error) {
	lang = resolveLang(lang)
	start := c.d.Now()

	// Step 1 — series_cache → series_id. Same invariant as 215.
	cache, err := c.d.SeriesCache.Get(ctx, instanceName, sonarrSeriesID)
	if err != nil {
		return nil, fmt.Errorf("series_cache lookup: %w", err)
	}
	if cache.SeriesID == nil || *cache.SeriesID == 0 {
		return nil, fmt.Errorf("series_cache lookup: %w", ports.ErrNotFound)
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

	out := &CastPage{
		Instance:       instanceName,
		SonarrSeriesID: sonarrSeriesID,
		SeriesID:       seriesID,
		Lang:           lang,
		Summary:        buildSeriesSummary(canon),
	}

	// Step 3 — total_episode_count. One indexed count query.
	// Failure is non-fatal: zero is a valid value (cold series,
	// no episode hydration). Logged so it shows up in obs.
	total, cerr := c.d.EpisodesCount.CountBySeries(ctx, seriesID)
	if cerr != nil {
		c.d.Logger.WarnContext(ctx, "cast_total_episode_count_failed",
			slog.String("instance_name", instanceName),
			slog.Int64("series_id", seriesID),
			slog.String("error", cerr.Error()))
		total = 0
	}
	out.TotalEpisodeCount = total

	// Step 4 — load cast (kind='cast'). Repository orders by
	// (kind ASC, credit_order ASC NULLS LAST) — exactly the
	// shape this list wants.
	castCredits, err := c.d.SeriesPeople.ListBySeries(ctx, seriesID, people.SeriesCreditCast)
	if err != nil {
		return nil, fmt.Errorf("list cast: %w", err)
	}

	// Step 5 — load crew (kind='crew'). Same repository call.
	// Default ordering is (kind ASC, credit_order ASC) — wrong
	// shape for crew; we re-sort by (department, name) post-join
	// below.
	crewCredits, err := c.d.SeriesPeople.ListBySeries(ctx, seriesID, people.SeriesCreditCrew)
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
		persons, perr := c.d.People.ListByIDs(ctx, personIDs)
		if perr != nil {
			return nil, fmt.Errorf("list people: %w", perr)
		}
		for _, p := range persons {
			personByID[p.ID] = p
		}
	}

	// Step 7 — per-person in_library cache. One probe per unique
	// person; bounded (typical N ≤ 100, indexed on
	// `person_credits_person`). Computed once even though the
	// person may appear in both cast and crew (or in multiple
	// crew rows for different jobs). The cache is keyed by
	// PersonID — this is just a lookup table, NOT a dedupe of
	// the crew list itself.
	inLibraryCache := map[int64]bool{}
	for _, pid := range personIDs {
		inLibraryCache[pid] = c.probeInLibrary(ctx, pid, seriesID)
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

	out.SyncedAt = c.d.Now()

	c.d.Logger.InfoContext(ctx, "series_cast_composed",
		slog.String("instance_name", instanceName),
		slog.Int("sonarr_series_id", sonarrSeriesID),
		slog.Int64("series_id", seriesID),
		slog.Int("cast_count", len(out.Cast)),
		slog.Int("crew_count", len(out.Crew)),
		slog.Int("total_episode_count", out.TotalEpisodeCount),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	return out, nil
}

// probeInLibrary returns true when the person appears as cast or
// crew on at least one OTHER live library series. The current
// series is excluded so the frontend's "what else are they in?"
// affordance never renders a self-link.
//
// Implementation mirrors 215's recommendations branch in_library
// pattern: ListByPerson → for each TV credit → GetByTMDBID on the
// canon row matching the TMDB media id → ListBySeriesID on canon
// series.id. N+1 bounded; all calls hit indexed paths.
//
// Errors are non-fatal — surface as "not in library" + warn log.
// Same posture as the G-1 recommendations branch (best-effort,
// degraded gracefully).
func (c *CastComposer) probeInLibrary(ctx context.Context, personID int64, currentSeriesID int64) bool {
	credits, err := c.d.PersonCredits.ListByPerson(ctx, personID)
	if err != nil {
		if !errors.Is(err, ports.ErrNotFound) {
			c.d.Logger.WarnContext(ctx, "cast_in_library_probe_failed",
				slog.Int64("person_id", personID),
				slog.String("error", err.Error()))
		}
		return false
	}
	for _, pc := range credits {
		if pc.MediaType != "tv" {
			continue
		}
		seriesID, ok := c.resolveSeriesByTMDB(ctx, pc.TMDBMediaID)
		if !ok {
			continue
		}
		// Self-link suppression: skip the current series so the
		// boolean represents "in ANYTHING ELSE we own?".
		if seriesID == currentSeriesID {
			continue
		}
		caches, lerr := c.d.SeriesCacheLookup.ListBySeriesID(ctx, seriesID)
		if lerr != nil {
			continue
		}
		if len(caches) > 0 {
			return true
		}
	}
	return false
}

// resolveSeriesByTMDB is a thin helper that looks up canon
// series.id by TMDB id via the SeriesPort. Cold path: most cast
// members have credits only on series the operator doesn't own.
// The lookup misses cheaply (one indexed `series_tmdb_id`
// partial-unique probe) and returns false.
func (c *CastComposer) resolveSeriesByTMDB(ctx context.Context, tmdbID int) (int64, bool) {
	if tmdbID == 0 {
		return 0, false
	}
	canon, err := c.d.Series.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		return 0, false
	}
	return canon.ID, true
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
func buildSeriesSummary(c series.Canon) SeriesSummary {
	s := SeriesSummary{
		Title:       c.Title,
		PosterAsset: c.PosterAsset,
		Status:      mapStatusToken(c.Status, c.InProduction),
	}
	if c.Year != nil {
		ys := *c.Year
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
