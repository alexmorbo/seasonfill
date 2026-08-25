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
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
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

// CollectionI18nWriter is the F-08 S2 localized-collection write seam. Production
// impl: *enrichpersistence.CollectionTextsRepository. Two methods: resolve the
// collections LOCAL PK from the TMDB id (collection_texts.collection_id FK
// target), then COALESCE-upsert a (collection_id, language) text row. nil-OK on
// the worker deps — when nil the collection-populate step writes NO localized
// texts (exact pre-S2 behavior).
type CollectionI18nWriter interface {
	IDByTMDBCollectionID(ctx context.Context, tmdbCollectionID int) (int64, error)
	UpsertCollectionTexts(ctx context.Context, collectionID int64, language, name, overview string, enrichedAt time.Time) error
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
	// Texts is the localized collection_texts writer (F-08 S2). Nil-OK: nil
	// disables the per-language text write entirely (pre-S2 behavior).
	Texts CollectionI18nWriter
	// Clock is an injectable UTC time source for enriched_at stamping. Nil-OK:
	// nil falls back to time.Now (mirror of MovieWorker's Clock default).
	Clock    func() time.Time
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

	// Localized collection texts — one row per supported UI language (F-08 S2,
	// mirror of the movie worker's per-language movie_i18n fan-out at
	// movie_worker.go:173-205). The SINGLE GetCollection fetch above carries
	// translations (append_to_response=translations): the base language uses the
	// response root (resp.Name/resp.Overview, already localized to w.baseLang);
	// every OTHER language is pulled from resp.Translations. Two guards mirror the
	// movie worker exactly: (a) skip a language TMDB has no translation for (never
	// mint an all-empty row); (b) skip when the localized NAME is blank
	// (no-progress write). nil Texts writer ⇒ block skipped (pre-S2 behavior).
	// i18n write failures are COLLECTED into errs (best-effort, consistent with the
	// per-part policy above) — one bad language never aborts the populate.
	if w.deps.Texts != nil {
		collectionID, idErr := w.deps.Texts.IDByTMDBCollectionID(ctx, collectionTMDBID)
		if idErr != nil {
			errs = append(errs, fmt.Errorf("resolve collection pk tmdb=%d: %w", collectionTMDBID, idErr))
		} else {
			now := w.now()
			trByLang := collectionTranslationsByLang(resp)
			baseShort := shortLang(w.baseLang)
			for _, lang := range locale.SupportedUserLanguages {
				name, overview := resp.Name, resp.Overview
				if shortLang(lang) != baseShort {
					tr, ok := trByLang[shortLang(lang)]
					if !ok {
						continue
					}
					name, overview = tr.Title, tr.Overview
					if name == "" {
						continue
					}
				}
				if terr := w.deps.Texts.UpsertCollectionTexts(ctx, collectionID, lang, name, overview, now); terr != nil {
					errs = append(errs, fmt.Errorf("upsert collection_texts pk=%d (%s): %w", collectionID, lang, terr))
				}
			}
		}
	}

	w.deps.Logger.InfoContext(ctx, "enrichment.movie.collection_populated",
		slog.Int("collection_id", collectionTMDBID),
		slog.Int("parts_total", len(parts)),
		slog.Int("parts_failed", len(errs)),
	)
	return errors.Join(errs...)
}

// now returns the UTC enriched_at stamp for collection_texts, using the
// injectable Clock when set (tests) else time.Now (mirror of MovieWorker).
func (w *MovieCollectionWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

// collectionTranslationsByLang indexes append_to_response=translations by bare
// 2-letter language code (shortLang) → localized text fields. Mirror of
// movieTranslationsByLang (movie_worker.go:374). A collection translation's
// localized NAME is data.title (the movie shape), so MovieTranslationData maps
// 1:1. Empty map when the response carries no translations sub-resource.
func collectionTranslationsByLang(resp *tmdb.CollectionResponse) map[string]tmdb.MovieTranslationData {
	out := map[string]tmdb.MovieTranslationData{}
	if resp == nil || resp.Translations == nil {
		return out
	}
	for i := range resp.Translations.Translations {
		t := &resp.Translations.Translations[i]
		out[shortLang(t.ISO6391)] = t.Data
	}
	return out
}
