// Package dto — Story 217 (H-2) person detail page payload.
// Backs GET /api/v1/people/:tmdbId — the screen reached from the
// Series Detail Cast strip / Cast & Crew page / Recommendations.
// Distinct from the cast page DTO (dto/cast.go): that one carries
// per-series cast rows; this one carries per-person filmography
// projected against the operator's library.
package dto

// PersonDetailResponse is the full person page payload returned
// by GET /api/v1/people/:tmdbId?lang=&sort=. Sections map onto
// design brief §4.3 (hero) + §4.4 (bio / library / other credits).
type PersonDetailResponse struct {
	// Person is the canonical person row — hero metadata.
	Person PersonInfo `json:"person"`
	// Biography is the resolved free-form biography prose; empty
	// when no person_biographies row exists for any language.
	// BioLanguage echoes the language of the resolved row so the
	// frontend can render an `EN` chip when fallback fired.
	Biography   string `json:"biography,omitempty"`
	BioLanguage string `json:"bio_language,omitempty" example:"en-US"`
	// Sync is the per-source hydration timestamp drawn from
	// sync_log(entity_type=person, source=tmdb_person). Omitted
	// when no row exists — `degraded[]` then carries
	// `"tmdb_person"`.
	Sync *SyncInfo `json:"sync,omitempty"`
	// LibraryCredits is the JOIN of person_credits × canon series
	// × live series_cache. Sorted per the `sort` query param
	// (default "recent" = series.last_aired_at DESC).
	LibraryCredits []LibraryCreditEntry `json:"library_credits"`
	// OtherCredits is every person_credits row NOT resolved to a
	// library series — TMDB-only metadata. Sorted by
	// (year DESC, title ASC) — the repository's default ordering.
	OtherCredits []OtherCreditEntry `json:"other_credits"`
	// Degraded carries any source that's never-synced / errored /
	// stale per PRD §5.6 rules. The H-2 page only journals
	// tmdb_person; degraded[] is either `[]` or
	// `["tmdb_person"]`.
	Degraded []string `json:"degraded"`
}

// PersonInfo is the canonical person row. Mirrors PRD §5.3 row
// "people" — instance-independent, natural key tmdb_id. Name and
// OriginalName are NOT localised (TMDB doesn't translate names
// reliably).
type PersonInfo struct {
	ID                 int64    `json:"id" example:"7"`
	TMDBID             *int     `json:"tmdb_id,omitempty" example:"4495"`
	Name               string   `json:"name" example:"Pedro Pascal"`
	OriginalName       *string  `json:"original_name,omitempty"`
	Birthday           *string  `json:"birthday,omitempty" example:"1975-04-02"`
	Deathday           *string  `json:"deathday,omitempty"`
	PlaceOfBirth       *string  `json:"place_of_birth,omitempty" example:"Santiago, Chile"`
	KnownForDepartment *string  `json:"known_for_department,omitempty"`
	ProfileAsset       *string  `json:"profile_asset,omitempty"`
	Popularity         *float64 `json:"popularity,omitempty"`
}

// SyncInfo is the "Source: TMDB · updated N ago" microcopy line
// under the biography (design-handoff Q9).
type SyncInfo struct {
	Source   string `json:"source" example:"tmdb_person"`
	SyncedAt string `json:"synced_at" example:"2026-06-10T03:14:00Z"`
}

// LibraryCreditEntry is one library_credit — a row of
// person_credits whose tmdb_media_id resolves to a canon `series`
// row that has at least one live `series_cache` reference.
type LibraryCreditEntry struct {
	SeriesID      int64    `json:"series_id" example:"42"`
	TMDBID        *int     `json:"tmdb_id,omitempty" example:"100"`
	Title         string   `json:"title" example:"The Last of Us"`
	Year          *int     `json:"year,omitempty" example:"2023"`
	CharacterName *string  `json:"character_name,omitempty"`
	EpisodeCount  *int     `json:"episode_count,omitempty" example:"9"`
	Kind          string   `json:"kind" example:"cast"`
	RoleLabel     string   `json:"role_label" example:"Joel Miller"`
	PosterAsset   *string  `json:"poster_asset,omitempty"`
	Instances     []string `json:"instances"`
}

// OtherCreditEntry is one TMDB-only credit (no canon series row
// OR canon row with no live series_cache references). Shape
// projects the upstream TMDB metadata directly — no library
// attribution.
type OtherCreditEntry struct {
	TMDBMediaID   int      `json:"tmdb_media_id" example:"999"`
	MediaType     string   `json:"media_type" example:"tv"`
	Title         string   `json:"title"`
	Year          *int     `json:"year,omitempty"`
	CharacterName *string  `json:"character_name,omitempty"`
	Kind          string   `json:"kind" example:"cast"`
	RoleLabel     string   `json:"role_label"`
	PosterPath    *string  `json:"poster_path,omitempty"`
	VoteAverage   *float64 `json:"vote_average,omitempty"`
}
