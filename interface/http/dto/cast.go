// Package dto — Story 216 (H-1) full cast & crew page payload.
// Distinct from the top-10 `CastMember` used by the Series
// Detail composite (dto/series_detail.go): that one is the
// carousel projection (no `in_library`, no `tmdb_id` shorthand).
// The cast page wants richer per-row metadata for the Main /
// Recurring / Guest derivation and the "click to see what else
// this person is in" affordance — own file, own types.
package dto

// SeriesCastResponse is the full cast & crew payload returned by
// GET /api/v1/instances/:name/series/:id/cast?lang=. The shape
// matches the H-1 design brief sections §3.4 (tabs Cast + Crew)
// and §4.4 ("Also in your library" reuses InLibrary).
type SeriesCastResponse struct {
	// Instance is the Sonarr instance the request hit. Echoed so
	// the client can disambiguate when a person is in multiple
	// instances.
	Instance string `json:"instance" example:"alpha"`
	// SonarrSeriesID is the Sonarr-side id from the URL.
	SonarrSeriesID int `json:"sonarr_series_id" example:"1"`
	// SeriesID is the resolved canonical series.id.
	SeriesID int64 `json:"series_id" example:"42"`
	// Lang is the BCP-47 language code requested. Accepted for
	// forward compatibility (H-2 person page localises Biography);
	// the cast list itself has no per-language fields in v1.
	Lang string `json:"lang" example:"en-US"`
	// TotalEpisodeCount is the count of episode rows for the
	// resolved series_id. Used by the frontend as the divisor for
	// Main / Recurring / Guest badges:
	//   episode_count / total_episode_count > 0.5  -> Main
	//   episode_count / total_episode_count > 0.1  -> Recurring
	//   else                                       -> Guest
	// (design-handoff Q3). Zero when no episodes are hydrated
	// yet — frontend treats badges as N/A.
	TotalEpisodeCount int `json:"total_episode_count" example:"62"`
	// Cast is the full cast list, sorted by credit_order ASC NULLS
	// LAST. Empty slice when no series_people kind='cast' rows.
	Cast []CastPageMember `json:"cast"`
	// Crew is the full crew list, sorted by (department ASC, name
	// ASC). Per-person duplicates with distinct jobs are
	// preserved — frontend dedups visually. Empty slice when no
	// series_people kind='crew' rows.
	Crew []CrewPageMember `json:"crew"`
	// SyncedAt is the request timestamp (server-side now()); the
	// frontend uses it for the "synced Xs ago" microcopy.
	SyncedAt string `json:"synced_at" example:"2026-06-13T12:00:00Z"`
}

// CastPageMember is one cast row of the full-page list. Distinct
// from dto.CastMember (the top-10 carousel) — that one omits
// in_library and lives in the composite series detail document.
type CastPageMember struct {
	// PersonID is the canon people.id. Frontend uses it for the
	// /person/:tmdbId link (resolved via TMDBID where present).
	PersonID int64 `json:"person_id"`
	// TMDBID is the TMDB person id. nil when the person was
	// onboarded from a non-TMDB source (rare; today every cast
	// member comes from TMDB aggregate_credits).
	TMDBID *int `json:"tmdb_id,omitempty"`
	// Name is the person's display name (locale-independent —
	// TMDB doesn't translate names reliably, PRD §5.3 row
	// "people").
	Name string `json:"name" example:"Pedro Pascal"`
	// ProfileAsset is the media_assets.hash for the person's
	// profile photo. nil when the person has no profile_path
	// (frontend renders a monogram placeholder).
	ProfileAsset *string `json:"profile_asset,omitempty"`
	// CharacterName is the role on this series. nil when the
	// TMDB credit carries no character (rare for kind=cast).
	CharacterName *string `json:"character_name,omitempty"`
	// CreditOrder is the TMDB billing order. nil when TMDB didn't
	// emit one; the composer sorts NULLS LAST.
	CreditOrder *int `json:"credit_order,omitempty"`
	// EpisodeCount is the number of episodes this person appeared
	// in on this series (TMDB aggregate_credits[*].
	// total_episode_count). nil when TMDB returned no count.
	// The frontend derives Main / Recurring / Guest by comparing
	// against TotalEpisodeCount.
	EpisodeCount *int `json:"episode_count,omitempty"`
	// InLibrary is true when the person appears as cast or crew
	// on at least one OTHER series in this seasonfill's library
	// (any active series_cache row, any instance). Excludes the
	// current series so the "what else are they in?" affordance
	// doesn't render a self-link.
	InLibrary bool `json:"in_library"`
}

// CrewPageMember is one crew row of the full-page list. Mirrors
// CastPageMember but carries department + job instead of
// character.
type CrewPageMember struct {
	PersonID     int64   `json:"person_id"`
	TMDBID       *int    `json:"tmdb_id,omitempty"`
	Name         string  `json:"name"`
	ProfileAsset *string `json:"profile_asset,omitempty"`
	// Department is the TMDB department classification
	// ("Production", "Writing", "Directing", "Editorial", ...).
	// nil when TMDB didn't emit one.
	Department *string `json:"department,omitempty"`
	// Job is the TMDB job title within the department
	// ("Executive Producer", "Director", "Writer"). nil when
	// missing. One person with multiple jobs on the same series
	// produces multiple CrewPageMember entries with the same
	// PersonID but distinct Job values — frontend dedupes
	// visually per design brief §3.4 (top-2 jobs joined by ·,
	// rest in tooltip).
	Job *string `json:"job,omitempty"`
	// EpisodeCount as on CastPageMember; semantics identical
	// (per-(person, job) aggregate count).
	EpisodeCount *int `json:"episode_count,omitempty"`
	InLibrary    bool `json:"in_library"`
}
