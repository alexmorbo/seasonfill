// Package people composes the canonical Person page payload (PRD
// v4 §5.7 row "/people/:tmdbId" + design brief §4). The use case
// is read-mostly with one write side-effect (stub-on-demand
// enqueue via the enrichment dispatcher). All repository access
// goes through narrow ports declared here — the composer never
// depends on a concrete repository type, matching the seriesdetail
// package conventions.
package people

import (
	"context"

	"github.com/alexmorbo/seasonfill/application/enrichment"
	domenrich "github.com/alexmorbo/seasonfill/domain/enrichment"
	dompeople "github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// PeopleReader resolves a canon people row by TMDB id and
// populates the resolved Biography + BiographyLanguage from
// person_biographies via the shared §5.6 fallback helper.
//
// The production impl is *repositories.PeopleRepository — both
// GetByTMDBID (returns the row WITHOUT biography) and Get
// (returns the row WITH biography). The composer needs the
// biography-resolving Get, but keyed by TMDB id rather than
// people.id; the cmd-server adapter performs the two-step
// (tmdb→id, then id+lang→Person with bio).
type PeopleReader interface {
	GetByTMDBID(ctx context.Context, tmdbID int) (dompeople.Person, error)
	GetWithBio(ctx context.Context, id int64, language string) (dompeople.Person, error)
}

// PersonCreditsReader returns every person_credits row for the
// given person, ordered by (year DESC, title ASC) — the
// repository's default ordering. The composer walks these rows
// to classify into library_credits vs other_credits.
type PersonCreditsReader interface {
	ListByPerson(ctx context.Context, personID int64) ([]dompeople.PersonCredit, error)
}

// SeriesByTMDBLookup resolves a TMDB media id to the canon
// series.id (via the partial-unique-index `series_tmdb_id WHERE
// tmdb_id IS NOT NULL`). The composer uses this to bridge
// person_credits → canon series → series_cache.
type SeriesByTMDBLookup interface {
	GetByTMDBID(ctx context.Context, tmdbID int) (series.Canon, error)
}

// SeriesCacheLookup returns the live series_cache rows for a
// canon series.id (deleted_at IS NULL). Empty result → the canon
// row is a stub / recommendation, not a library credit.
type SeriesCacheLookup interface {
	ListBySeriesID(ctx context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error)
}

// SyncLogLookup retrieves the latest sync_log row for
// (entity_type, entity_id, source). The composer reads the
// (person, tmdb_person) row for the "Source: TMDB · updated N
// days ago" microcopy AND for the degraded[] rule-1/rule-2
// evaluation.
type SyncLogLookup interface {
	GetLastSync(ctx context.Context, entityType domenrich.EntityType, entityID int64, source domenrich.Source) (domenrich.SyncLog, error)
}

// PersonEnqueuer is the write seam for stub-on-demand. The H-2
// path enqueues a PriorityHot job for stub persons and returns
// immediately — no wait. The dispatcher's Enqueue is
// fire-and-forget by contract (non-blocking, dedupes, never
// returns an error). Production impl is
// *enrichment.DispatcherImpl from the enrichment package.
type PersonEnqueuer interface {
	Enqueue(kind enrichment.EntityKind, id int64, p enrichment.Priority)
}

// MediaResolver narrows seriesdetail.MediaResolver to the methods the
// people use case calls. Kept as an interface so tests can pass a
// stub; the wiring layer hands the concrete *seriesdetail.MediaResolver.
// Story 316 added ResolveSync for the hero portrait on-demand fetch.
type MediaResolver interface {
	Resolve(ctx context.Context, rawPath *string, size, kind string) *string
	ResolveSync(ctx context.Context, rawPath *string, size, kind string) *string
}
