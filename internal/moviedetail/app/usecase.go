// Package app assembles the read-only movie-detail aggregate backing
// GET /api/v1/movies/:tmdb_id (Ф6-R-6a). It is the movie analog of the
// seriesdetail overview slice but sourced ENTIRELY from local repos (canon +
// movie_i18n + collection + per-instance membership) — no live TMDB call. Cast /
// recommendations / ratings sub-endpoints are deferred (no movie composer built).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// CanonReader resolves a movie canon by tmdb id. Impl: *enrichpersistence.MovieRepository.
type CanonReader interface {
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (movie.Canon, error)
}

// I18nReader resolves the localized row. Impl: *enrichpersistence.MovieI18nReadRepository.
type I18nReader interface {
	Get(ctx context.Context, movieID domain.MovieID, lang string) (enrichpersistence.MovieI18nRow, error)
}

// CollectionReader resolves the franchise collection canon. Impl:
// *enrichpersistence.MovieCollectionsRepository.GetByTMDBCollectionID.
type CollectionReader interface {
	GetByTMDBCollectionID(ctx context.Context, tmdbCollectionID int) (movie.CollectionCanon, error)
}

// MembershipReader lists ACTIVE per-instance states for a movie id. Impl:
// *catalogpersistence.MovieStatesRepository.ListActiveByMovieID.
type MembershipReader interface {
	ListActiveByMovieID(ctx context.Context, movieID domain.MovieID) ([]movie.StateEntry, error)
}

// GenresReader lists a movie's genre ids + resolves localized names via the
// shared §5.6 two-tier fallback (Ф2.5a). Impl: *enrichpersistence.GenresRepository.
type GenresReader interface {
	ListByMovie(ctx context.Context, movieID domain.MovieID) ([]int64, error)
	ListByIDsWithFallback(ctx context.Context, ids []int64, language string) ([]taxonomy.Genre, error)
}

// KeywordsReader — same shape as GenresReader for keywords (Ф2.5a). Impl:
// *enrichpersistence.KeywordsRepository.
type KeywordsReader interface {
	ListByMovie(ctx context.Context, movieID domain.MovieID) ([]int64, error)
	ListByIDsWithFallback(ctx context.Context, ids []int64, language string) ([]taxonomy.Keyword, error)
}

// Detail is the assembled aggregate. Localized fields fall back to canon.
type Detail struct {
	Canon      movie.Canon
	Title      string  // localized > canon.Title
	Overview   *string // localized overview (nil when none)
	Tagline    *string
	Poster     *string // localized poster > canon.PosterAsset
	Backdrop   *string
	Collection *movie.CollectionCanon // nil when the movie has no collection_id
	Library    []LibraryMembership
	Genres     []taxonomy.Genre   // Ф2.5a localized genre chips (join order)
	Keywords   []taxonomy.Keyword // Ф2.5a localized keyword chips (keyword_id order)
	Degraded   []string           // "movie_i18n" when no localized row for lang
}

// LibraryMembership is one active per-instance Radarr membership row.
type LibraryMembership struct {
	InstanceName  string
	RadarrMovieID int
	Monitored     bool
	HasFile       bool
	Availability  *string
	SizeOnDisk    int64
}

// StaleMarker marks a movie for re-enrichment on the next MovieRefreshScheduler
// tick (Ф1.2 on-read hydration). Impl: *enrichpersistence.MovieRepository.
type StaleMarker interface {
	MarkStaleForReenrich(ctx context.Context, movieID domain.MovieID, now time.Time) error
}

// UseCase assembles the movie-detail aggregate from local read ports. The
// hydration trigger (stale/now/log) is optional — wired via WithHydrationTrigger;
// when stale is nil the trigger is a no-op (New alone leaves reads unchanged).
type UseCase struct {
	canon      CanonReader
	i18n       I18nReader
	collection CollectionReader
	membership MembershipReader

	genres   GenresReader   // nil-OK: Genres stays empty when unwired
	keywords KeywordsReader // nil-OK: Keywords stays empty when unwired

	stale StaleMarker
	now   func() time.Time
	log   *slog.Logger
}

// New constructs the movie-detail usecase over its four read ports. The on-read
// hydration trigger is opt-in via WithHydrationTrigger (WithLocalizer precedent),
// so existing callers stay unchanged and the trigger stays a no-op until wired.
func New(canon CanonReader, i18n I18nReader, collection CollectionReader, membership MembershipReader) *UseCase {
	return &UseCase{canon: canon, i18n: i18n, collection: collection, membership: membership}
}

// WithTaxonomy enables the Ф2.5a genres+keywords enrichment. Both readers are
// used together; either nil leaves both slices empty (no partial state). Returns
// the receiver for chaining in the wiring.
func (uc *UseCase) WithTaxonomy(genres GenresReader, keywords KeywordsReader) *UseCase {
	uc.genres = genres
	uc.keywords = keywords
	return uc
}

// WithHydrationTrigger enables the Ф1.2 on-read mark-stale nudge. now nil →
// time.Now; log nil → a default "http" domain logger. Returns the receiver for
// chaining in the wiring.
func (uc *UseCase) WithHydrationTrigger(stale StaleMarker, now func() time.Time, log *slog.Logger) *UseCase {
	uc.stale = stale
	if now == nil {
		now = time.Now
	}
	uc.now = now
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "http")
	}
	uc.log = log
	return uc
}

// Get assembles the detail for a tmdb id. ports.ErrNotFound bubbles when no
// canon row exists (→ 404). lang selects the movie_i18n row; a missing localized
// row degrades to canon fields (Degraded=["movie_i18n"]) — never an error.
func (uc *UseCase) Get(ctx context.Context, tmdbID domain.TMDBID, lang string) (Detail, error) {
	canon, err := uc.canon.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		return Detail{}, err // ports.ErrNotFound bubbles
	}
	d := Detail{Canon: canon, Title: canon.Title, Poster: canon.PosterAsset, Backdrop: canon.BackdropAsset}

	if lang != "" {
		row, ierr := uc.i18n.Get(ctx, canon.ID, lang)
		switch {
		case ierr == nil:
			if row.Title != nil && *row.Title != "" {
				d.Title = *row.Title
			}
			if row.Overview != nil && *row.Overview != "" {
				d.Overview = row.Overview
			}
			if row.Tagline != nil && *row.Tagline != "" {
				d.Tagline = row.Tagline
			}
			if row.Poster != nil {
				d.Poster = row.Poster
			}
			if row.Backdrop != nil {
				d.Backdrop = row.Backdrop
			}
		case errors.Is(ierr, ports.ErrNotFound):
			d.Degraded = append(d.Degraded, "movie_i18n")
		default:
			return Detail{}, fmt.Errorf("movie detail: i18n: %w", ierr)
		}
	}

	if canon.CollectionID != nil {
		col, cerr := uc.collection.GetByTMDBCollectionID(ctx, *canon.CollectionID)
		if cerr == nil {
			d.Collection = &col
		} else if !errors.Is(cerr, ports.ErrNotFound) {
			return Detail{}, fmt.Errorf("movie detail: collection: %w", cerr)
		}
	}

	states, merr := uc.membership.ListActiveByMovieID(ctx, canon.ID)
	if merr != nil {
		return Detail{}, fmt.Errorf("movie detail: membership: %w", merr)
	}
	for _, s := range states {
		d.Library = append(d.Library, LibraryMembership{
			InstanceName:  string(s.InstanceName),
			RadarrMovieID: s.RadarrMovieID,
			Monitored:     s.Monitored,
			HasFile:       s.HasFile,
			Availability:  s.Availability,
			SizeOnDisk:    s.SizeOnDiskBytes,
		})
	}

	uc.loadTaxonomy(ctx, &d, canon.ID, lang)
	uc.maybeTriggerHydration(ctx, canon)
	return d, nil
}

// loadTaxonomy fills d.Genres / d.Keywords from the movie join tables (Ф2.5a).
// Two reads per kind: the join ids (position order) then the batch i18n
// fallback resolve; results are re-projected into join order via a map (the
// batch reader orders by id-ASC). Fail-open: a read error is logged at Warn and
// leaves the slice empty — taxonomy NEVER 500s the detail. No-op when the
// readers are unwired (genres/keywords nil).
func (uc *UseCase) loadTaxonomy(ctx context.Context, d *Detail, movieID domain.MovieID, lang string) {
	if uc.genres != nil {
		if ids, err := uc.genres.ListByMovie(ctx, movieID); err != nil {
			uc.logTaxonomyWarn(ctx, "movie_genres", movieID, err)
		} else if len(ids) > 0 {
			resolved, rerr := uc.genres.ListByIDsWithFallback(ctx, ids, lang)
			if rerr != nil {
				uc.logTaxonomyWarn(ctx, "movie_genres_i18n", movieID, rerr)
			} else {
				byID := make(map[int64]taxonomy.Genre, len(resolved))
				for _, g := range resolved {
					byID[g.ID] = g
				}
				for _, id := range ids {
					if g, ok := byID[id]; ok {
						d.Genres = append(d.Genres, g)
					}
				}
			}
		}
	}

	if uc.keywords != nil {
		if ids, err := uc.keywords.ListByMovie(ctx, movieID); err != nil {
			uc.logTaxonomyWarn(ctx, "movie_keywords", movieID, err)
		} else if len(ids) > 0 {
			resolved, rerr := uc.keywords.ListByIDsWithFallback(ctx, ids, lang)
			if rerr != nil {
				uc.logTaxonomyWarn(ctx, "movie_keywords_i18n", movieID, rerr)
			} else {
				byID := make(map[int64]taxonomy.Keyword, len(resolved))
				for _, k := range resolved {
					byID[k.ID] = k
				}
				for _, id := range ids {
					if k, ok := byID[id]; ok {
						d.Keywords = append(d.Keywords, k)
					}
				}
			}
		}
	}
}

// logTaxonomyWarn records a fail-open taxonomy read error. Silent when the
// logger is unwired (New without WithHydrationTrigger — the test path).
func (uc *UseCase) logTaxonomyWarn(ctx context.Context, section string, movieID domain.MovieID, err error) {
	if uc.log == nil {
		return
	}
	uc.log.WarnContext(ctx, "moviedetail.taxonomy.read_error",
		slog.String("section", section),
		slog.Int64("movie_id", int64(movieID)),
		slog.String("error", err.Error()),
	)
}

// maybeTriggerHydration runs the pure section probe over the already-loaded
// canon and, if any section is stale AND the movie has a tmdb_id, bumps
// tmdb_changed_at so the next MovieRefreshScheduler tick re-enriches it. The
// nudge is a single guarded PK UPDATE (sub-ms) executed synchronously; a marker
// error is swallowed (logged at Warn) so the read NEVER fails — fail-open per
// the Radarr lesson. No-op when the trigger is unwired (stale nil) or the movie
// has no tmdb_id (a Radarr orphan the picker can never re-enrich anyway).
func (uc *UseCase) maybeTriggerHydration(ctx context.Context, canon movie.Canon) {
	if uc.stale == nil || canon.TMDBID == nil {
		return
	}
	now := time.Now
	if uc.now != nil {
		now = uc.now
	}
	if !AnyStale(MovieProbe(canon, now())) {
		return
	}
	if err := uc.stale.MarkStaleForReenrich(ctx, canon.ID, now()); err != nil && uc.log != nil {
		uc.log.WarnContext(ctx, "moviedetail.hydration.mark_stale_error",
			slog.Int64("movie_id", int64(canon.ID)),
			slog.String("error", err.Error()),
		)
	}
}
