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
		s == SourceTVDBResolve
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
