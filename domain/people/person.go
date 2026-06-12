// Package people carries the canonical people-domain value types
// (PRD v4 §5.3 + §8.1). Person is the canonical, instance-independent
// person entity (one row per real human, natural key tmdb_id); the
// localised biography prose lives on a separate (person_id, language)
// row read via the §5.6 fallback helper. SeriesCredit and
// EpisodeCredit (credit.go) materialise TMDB aggregate_credits /
// per-episode credits.
//
// Hydration mirrors domain/series.Hydration verbatim. Kept as a
// parallel local type so the people package has zero domain imports;
// callers that need cross-domain comparisons string-convert at the
// boundary.
package people

import "time"

// Hydration tracks how deeply a Person row has been enriched. Stub
// rows enter the schema from series_enrichment_worker (C-2) when an
// aggregate_credits entry references a person not yet seen; the
// person_enrichment_worker (C-3) lifts them to Full on demand or on
// background scan. Empty value is normalised to HydrationStub by the
// repository — defensive default for legacy code paths.
type Hydration string

const (
	HydrationStub Hydration = "stub"
	HydrationFull Hydration = "full"
)

// IsValid reports whether h is one of the two known levels. Empty
// strings are explicitly NOT valid — callers MUST normalise to
// HydrationStub before persisting.
func (h Hydration) IsValid() bool {
	return h == HydrationStub || h == HydrationFull
}

// Person is the canonical, instance-independent local person entity
// (PRD §5.3 row "people"). One row per real-world person, natural
// key tmdb_id when present. Biography is the resolved single-language
// string the repository populates via JOIN against person_biographies
// using the §5.6 fallback helper; the language of the resolved row
// is in BiographyLanguage. Writes go directly to
// PersonBiographiesRepository, NOT through these two fields — they
// are read-only projections on Person and the repository's Get path
// is the only writer.
//
// Every *string / *int field maps to a nullable column. Pointers
// (not zero values) make `nil = SQL NULL, zero = explicit 0/""`
// unambiguous on the merge-policy boundary (§5.4) — a TMDB worker
// writing `Popularity=ptr(0.0)` MUST be distinguishable from a worker
// that left popularity unset.
//
// Name and OriginalName stay on Person (NOT a people_names i18n
// table): TMDB does not localise person names reliably. See package
// header + the PeopleModel comment in models.go for the full
// rationale.
type Person struct {
	ID                 int64
	TMDBID             *int
	IMDBID             *string
	Hydration          Hydration
	Name               string
	OriginalName       *string
	Gender             *int
	Birthday           *time.Time
	Deathday           *time.Time
	PlaceOfBirth       *string
	KnownForDepartment *string
	Popularity         *float64
	ProfileAsset       *string
	// Biography is the resolved biography prose returned by
	// PeopleRepository.Get — read-only on Person. May be empty if no
	// row exists in person_biographies for any language.
	Biography string
	// BiographyLanguage is the language of the resolved Biography
	// row; empty when Biography is empty. Composer surfaces this so
	// UI can render an "EN"-tag when a non-requested language was
	// served by the §5.6 fallback path.
	BiographyLanguage string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// PersonBiography is one localised biography row of
// person_biographies. Mirrors the SeriesText / EpisodeText shape
// from story 203 — same (entity_id, language) PK form, same shared
// fallback helper. Writes go through
// PersonBiographiesRepository.Upsert; reads via GetWithFallback or
// Get.
type PersonBiography struct {
	PersonID  int64
	Language  string
	Biography *string
	UpdatedAt time.Time
}
