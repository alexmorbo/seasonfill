// Package enrichment carries the canonical typed discriminators for the
// enrichment-tracking columns + the legacy SyncLog struct kept as a
// deprecation alias during the D-3 cutover (story 464a). The new
// success/failure shape lives in enrichment_error.go + canon
// series.enrichment_*_synced_at columns; SyncLog stays callable in this
// kernel-only sub-story so workers + composer + people use case all
// compile while their consumers wait for the 464b rewrite.
//
// 464b will delete the Outcome enum + SyncLog struct (and this comment)
// once workers stop calling the legacy SyncLogRepo port.
package enrichment

import "time"

// Source is the typed discriminator on enrichment-tracking writes
// (enrichment_errors.source + external_ids' provider semantics). Each
// value names a hydration source — TMDB per entity type (series,
// season, person) and OMDb for IMDB-rating fallback.
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

// EntityType is the typed discriminator on enrichment_errors.entity_type
// AND on external_ids.entity_type (PRD §5.3 row "external_ids"). The
// two tables share the same domain — they reference the same canonical
// entities — so the type lives here, in the enrichment package, and is
// imported by both the enrichment_errors repository and the external_ids
// repository.
type EntityType string

const (
	EntityTypeSeries  EntityType = "series"
	EntityTypeSeason  EntityType = "season"
	EntityTypePerson  EntityType = "person"
	EntityTypeEpisode EntityType = "episode"
)

// IsValid reports whether e is one of the four known entity types.
// enrichment_errors uses series/season/person; external_ids uses
// series/person/episode. Both subsets are valid here.
func (e EntityType) IsValid() bool {
	return e == EntityTypeSeries || e == EntityTypeSeason ||
		e == EntityTypePerson || e == EntityTypeEpisode
}

// Outcome is the typed discriminator on the legacy sync_log.outcome
// column. RETAINED during the D-3 cutover (464a) so worker / composer /
// people-use-case bodies that still read SyncLog.Outcome keep
// compiling. The 464b rewrite drops this enum together with the
// SyncLog struct.
//
// NOTE (464a → 464b): success is now recorded as the canon row's
// enrichment_*_synced_at column being non-NULL; failure is now
// recorded in enrichment_errors. The pending/not_found semantics are
// gone — a pending state is "no row" (no journal write on enqueue);
// a not_found is "attempts > 5" (terminal in enrichment_errors).
type Outcome string

const (
	OutcomePending  Outcome = "pending"
	OutcomeOK       Outcome = "ok"
	OutcomeError    Outcome = "error"
	OutcomeNotFound Outcome = "not_found"
)

// IsValid reports whether o is one of the four known outcomes.
//
// NOTE (464a → 464b): see Outcome — retired together with SyncLog.
func (o Outcome) IsValid() bool {
	return o == OutcomePending || o == OutcomeOK ||
		o == OutcomeError || o == OutcomeNotFound
}

// SyncLog is the legacy sync_log row shape. RETAINED during the D-3
// cutover (464a) so worker / composer / people-use-case bodies that
// still read this struct keep compiling. Every production write path
// now goes through SyncLogStub (panic at request time) — no row ever
// reaches the database. The 464b rewrite deletes this struct along
// with the SyncLogRepo / SyncLogPort interfaces.
//
// NOTE (464a → 464b): use enrichment_errors.EnrichmentError for
// failure tracking and the canon series.enrichment_*_synced_at column
// for success freshness.
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
