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

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichment "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
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
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (dompeople.Person, error)
	GetWithBio(ctx context.Context, id int64, language string) (dompeople.Person, error)
}

// PersonCreditsReader returns every person_credits row for the
// given person, ordered by (year DESC, title ASC) — the
// repository's default ordering. The composer walks these rows
// to classify into library_credits vs other_credits.
//
// character_name is resolved per language (requested → en-US → base
// person_credits.character_name) via person_credits_texts, so the person
// page localizes cast role labels the same way the series/cast page does.
type PersonCreditsReader interface {
	ListByPersonWithTextFallback(ctx context.Context, personID int64, lang string) ([]dompeople.PersonCredit, error)
}

// SeriesByTMDBLookup resolves a TMDB media id to the canon
// series.id (via the partial-unique-index `series_tmdb_id WHERE
// tmdb_id IS NOT NULL`). The composer uses this to bridge
// person_credits → canon series → series_cache.
type SeriesByTMDBLookup interface {
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (series.Canon, error)
}

// SeriesCacheLookup returns the live series_cache rows for a
// canon series.id (deleted_at IS NULL). Empty result → the canon
// row is a stub / recommendation, not a library credit.
type SeriesCacheLookup interface {
	ListBySeriesID(ctx context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error)
}

// SeriesTextsBatch resolves localized series titles (requested-lang →
// en-US) for a set of canon series ids in one round-trip. S-E3a — the
// person page's library credits read their display title from series_texts
// (canon no longer carries a title); the production impl is
// *enrichpersistence.SeriesTextsRepository (ListByIDsWithFallback). nil-OK:
// when unwired the use case falls back to canon OriginalTitle.
type SeriesTextsBatch interface {
	ListByIDsWithFallback(ctx context.Context, seriesIDs []domain.SeriesID, lang string) (map[domain.SeriesID]series.SeriesText, error)
}

// SeriesMediaTextsBatch resolves per-language poster raw paths
// (requested-lang → en-US) for a set of canon series ids. S-E3a — library
// credit posters come from series_media_texts (canon no longer carries
// poster_asset). Production impl:
// *enrichpersistence.SeriesMediaTextsRepository. nil-OK: unwired → nil
// poster (monogram).
type SeriesMediaTextsBatch interface {
	ListByIDsWithFallback(ctx context.Context, seriesIDs []domain.SeriesID, lang string) (map[domain.SeriesID]series.SeriesMediaText, error)
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
