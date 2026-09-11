// Package enrichment carries the canonical typed discriminators for the
// enrichment-tracking columns. The success/failure shape lives in
// enrichment_error.go + canon series.enrichment_*_synced_at columns +
// people.enrichment_synced_at. The pre-D-3 legacy `sync_log` table,
// its `Outcome` enum, and the `SyncLog` row struct have been retired —
// success is now stamped directly on the canon row's freshness column,
// and failure tracking lives in enrichment_errors.
package enrichment

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
	// SourceTMDBMovie journals /movie/{id} hydration failures (ADR-0025 Ф1).
	// Added three ADRs after the movie vertical shipped: ADR-0018 built movie
	// hydration as a parallel file set and never extended this enum, so
	// RecordFailure rejected every movie row at its own validator and seven
	// TMDB-deleted movies were re-pulled every ~15 minutes forever
	// (ADR-0025 Доказательство №2). Journalled, therefore it MUST pass
	// IsValid() — that is the ADR-0025 Р4-bis invariant, asserted by
	// TestADR0025_F0_JournalledSourcesValid.
	SourceTMDBMovie Source = "tmdb_movie"
	// SourceTVDBResolve journals the tvdb_id→tmdb_id resolver's terminal
	// not-found (W15-13). Isolated cooldown ledger: the retry-sweep
	// (ListDueForRetry) only sweeps tmdb_series/tmdb_person, and Degraded()
	// iterates a fixed canonicalOrder that excludes it, so a tvdb_resolve
	// row never surfaces as a degraded[] source or gets auto-retried.
	SourceTVDBResolve Source = "tvdb_resolve"
)

// IsValid reports whether s is one of the known sources. Empty
// strings are explicitly NOT valid — callers MUST supply a typed
// value before persisting.
func (s Source) IsValid() bool {
	return s == SourceTMDBSeries || s == SourceTMDBSeason ||
		s == SourceTMDBPerson || s == SourceOMDb ||
		s == SourceTMDBMovie || s == SourceTVDBResolve
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
	// EntityTypeMovie is the enrichment_errors discriminator for a canon
	// `movies` row (ADR-0025 Ф1). external_ids does not use it today; the two
	// tables share this enum, and no exhaustive switch over EntityType exists
	// anywhere in the codebase, so widening it is additive.
	EntityTypeMovie EntityType = "movie"
)

// IsValid reports whether e is one of the five known entity types.
// enrichment_errors uses series/season/person/movie; external_ids uses
// series/person/episode. Both subsets are valid here.
func (e EntityType) IsValid() bool {
	return e == EntityTypeSeries || e == EntityTypeSeason ||
		e == EntityTypePerson || e == EntityTypeEpisode ||
		e == EntityTypeMovie
}
