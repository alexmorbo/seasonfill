// movie_collection_worker.go — Ф6-R-5 collection populate step. Fetches a TMDB
// collection, upserts the `collections` row, and COALESCE stub-upserts every
// member movie into the movies canon so movies.collection_id ==
// collections.tmdb_collection_id linkage is populated. Driven fail-soft by
// MovieWorker.HandleForced (a failure here never fails the movie hydrate).
package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// CollectionTMDBClient is the collection detail-fetch seam. Production impl is a
// wiring-local adapter over the runtime-swappable TMDB holder (mirror of
// movieTMDBFromHolder); tests pass a fake.
type CollectionTMDBClient interface {
	GetCollection(ctx context.Context, id int64, language string) (*tmdb.CollectionResponse, error)
}

// MovieCollectionUpserter is the `collections` write seam. Production impl:
// *enrichpersistence.MovieCollectionsRepository.UpsertCollection.
type MovieCollectionUpserter interface {
	UpsertCollection(ctx context.Context, c movie.CollectionCanon) error
}

// MovieCollectionWorkerDeps — TMDB + Collections + Movies are required; BaseLang /
// Logger default. Movies is the SAME MovieCanonRepo the hydration worker holds
// (its COALESCE Upsert is reused for the part stub-upsert).
type MovieCollectionWorkerDeps struct {
	TMDB        CollectionTMDBClient
	Collections MovieCollectionUpserter
	Movies      MovieCanonRepo
	// Resolver pre-warms the collection header + part poster/backdrop raw paths
	// into media_assets at populate time (mirror of the movie worker), so the
	// read has stable sha256 handles instead of returning a pending sentinel on
	// first miss. Nil-OK / optional: nil skips the warm side-effect entirely.
	Resolver MediaResolver
	BaseLang string
	Logger   *slog.Logger
}

// MovieCollectionWorker satisfies MovieCollectionPopulator.
type MovieCollectionWorker struct {
	deps     MovieCollectionWorkerDeps
	baseLang string
}

// NewMovieCollectionWorker validates required deps and applies defaults.
func NewMovieCollectionWorker(deps MovieCollectionWorkerDeps) (*MovieCollectionWorker, error) {
	if deps.TMDB == nil {
		return nil, errors.New("movie collection worker: TMDB is required")
	}
	if deps.Collections == nil {
		return nil, errors.New("movie collection worker: Collections is required")
	}
	if deps.Movies == nil {
		return nil, errors.New("movie collection worker: Movies is required")
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	baseLang := deps.BaseLang
	if baseLang == "" {
		baseLang = tmdb.DefaultLanguage
	}
	return &MovieCollectionWorker{deps: deps, baseLang: baseLang}, nil
}

// PopulateCollection fetches /collection/{id}, upserts the collection row, and
// COALESCE stub-upserts every part into the movies canon. Per-part upsert errors
// are collected (never abort the batch) and joined into the return; the collection
// fetch / upsert errors return early. The CALLER (MovieWorker.HandleForced) treats
// the whole thing best-effort — an error here is logged, never fatal.
func (w *MovieCollectionWorker) PopulateCollection(ctx context.Context, collectionTMDBID int) error {
	if collectionTMDBID == 0 {
		return nil
	}
	resp, err := w.deps.TMDB.GetCollection(ctx, int64(collectionTMDBID), w.baseLang)
	if err != nil {
		return fmt.Errorf("populate collection %d: fetch: %w", collectionTMDBID, err)
	}
	if resp == nil {
		return fmt.Errorf("populate collection %d: nil response", collectionTMDBID)
	}

	headerCanon := tmdb.MapCollectionToCanon(resp)
	if err := w.deps.Collections.UpsertCollection(ctx, headerCanon); err != nil {
		return fmt.Errorf("populate collection %d: upsert collection: %w", collectionTMDBID, err)
	}

	// Media pre-warm side-effect (nil-OK): mint media_assets pending rows for the
	// collection header + each part poster so the read has stable sha256 handles
	// instead of a pending sentinel on first miss. Fire-and-forget (Resolve never
	// errors); never alters the errs/return behavior below.
	if w.deps.Resolver != nil {
		if headerCanon.PosterAsset != nil {
			_ = w.deps.Resolver.Resolve(ctx, headerCanon.PosterAsset, "w342", "poster_w342")
		}
		if headerCanon.BackdropAsset != nil {
			_ = w.deps.Resolver.Resolve(ctx, headerCanon.BackdropAsset, "w1280", "backdrop_w1280")
		}
	}

	var errs []error
	parts := tmdb.MapCollectionPartsToCanon(resp)
	for _, p := range parts {
		if w.deps.Resolver != nil && p.PosterAsset != nil {
			_ = w.deps.Resolver.Resolve(ctx, p.PosterAsset, "w342", "poster_w342")
		}
		if _, perr := w.deps.Movies.Upsert(ctx, p); perr != nil {
			tid := 0
			if p.TMDBID != nil {
				tid = int(*p.TMDBID)
			}
			errs = append(errs, fmt.Errorf("part tmdb=%d: %w", tid, perr))
		}
	}

	w.deps.Logger.InfoContext(ctx, "enrichment.movie.collection_populated",
		slog.Int("collection_id", collectionTMDBID),
		slog.Int("parts_total", len(parts)),
		slog.Int("parts_failed", len(errs)),
	)
	return errors.Join(errs...)
}
