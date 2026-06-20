// Package enrichment carries the canonical sync-journal value type +
// typed enums for the sync_log table (PRD v4 §5.5, §5.6, §7.1). The
// TTL table, IsStale helper, and NextAttemptAt backoff function are
// scope of story 207 (B-4 — merge/TTL/degraded policy); this story
// ships only the enums + struct the repository needs to validate
// inputs.
package enrichment

import "time"

// Source is the typed discriminator on sync_log.source. Each value
// names a hydration source — TMDB per entity type (series, season,
// person) and OMDb for IMDB-rating fallback. Workers pass these
// values to sync_log.Upsert; the repository string-converts at the
// SQL boundary so the column stays a plain `text` for portability.
type Source string

const (
	SourceTMDBSeries Source = "tmdb_series"
	SourceTMDBSeason Source = "tmdb_season"
	SourceTMDBPerson Source = "tmdb_person"
	SourceOMDb       Source = "omdb"
)

// IsValid reports whether s is one of the four known sources. Empty
// strings are explicitly NOT valid — callers MUST supply a typed
// value before persisting.
func (s Source) IsValid() bool {
	return s == SourceTMDBSeries || s == SourceTMDBSeason ||
		s == SourceTMDBPerson || s == SourceOMDb
}

// EntityType is the typed discriminator on sync_log.entity_type AND
// on external_ids.entity_type (PRD §5.3 row "external_ids"). The two
// tables share the same domain — they reference the same canonical
// entities — so the type lives here, in the enrichment package, and
// is imported by both the sync_log repository and the external_ids
// repository.
type EntityType string

const (
	EntityTypeSeries  EntityType = "series"
	EntityTypeSeason  EntityType = "season"
	EntityTypePerson  EntityType = "person"
	EntityTypeEpisode EntityType = "episode"
)

// IsValid reports whether e is one of the four known entity types.
// sync_log uses series/season/person; external_ids uses
// series/person/episode. Both subsets are valid here.
func (e EntityType) IsValid() bool {
	return e == EntityTypeSeries || e == EntityTypeSeason ||
		e == EntityTypePerson || e == EntityTypeEpisode
}

// Outcome is the typed discriminator on sync_log.outcome. Pending is
// the default-on-insert (worker has enqueued the entity but not yet
// fetched); OK marks a successful sync; Error marks a fetch failure
// (with backoff in next_attempt_at); NotFound marks the source's
// authoritative "no such entity" (e.g., a series with tmdb_id=NULL
// from Sonarr — TMDB has nothing to hydrate).
type Outcome string

const (
	OutcomePending  Outcome = "pending"
	OutcomeOK       Outcome = "ok"
	OutcomeError    Outcome = "error"
	OutcomeNotFound Outcome = "not_found"
)

// IsValid reports whether o is one of the four known outcomes.
func (o Outcome) IsValid() bool {
	return o == OutcomePending || o == OutcomeOK ||
		o == OutcomeError || o == OutcomeNotFound
}

// SyncLog is one row of the sync_log table (PRD §5.5, §7.1). Every
// sync worker writes one row per (entity, source) per attempt. The
// composer reads Outcome + SyncedAt to decide whether to surface a
// `degraded` entry; the dispatcher reads NextAttemptAt + Attempts
// to schedule retries.
//
// Pointer fields (*time.Time, *string, *int) map to nullable
// columns — nil means SQL NULL, zero means an explicit zero value
// (an error_detail="" string for example would be a worker bug, but
// the repository forwards it faithfully). Attempts is a plain int
// (non-nullable on the schema with DEFAULT 0).
//
// Workers MUST set EntityType / EntityID / Source on every call;
// the repository validates these are present + valid before
// touching SQL. Outcome defaults to OutcomePending on insert when
// the caller leaves it empty — same defensive default the schema
// has.
type SyncLog struct {
	EntityType    EntityType
	EntityID      int64
	Source        Source
	SyncedAt      *time.Time
	Outcome       Outcome
	ErrorDetail   *string
	ETag          *string
	Attempts      int
	NextAttemptAt *time.Time
	DurationMs    *int
	UpdatedAt     time.Time
}
