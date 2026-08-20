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

// CompaniesReader lists a movie's production-company ids (join order) and resolves
// the dict rows (Ф2.5b). Impl: *enrichpersistence.CompaniesRepository.
type CompaniesReader interface {
	ListByMovie(ctx context.Context, movieID domain.MovieID) ([]int64, error)
	ListByIDs(ctx context.Context, ids []int64) ([]taxonomy.ProductionCompany, error)
}

// MovieTrailerReader resolves the movie's single best trailer (Ф2.5b). ports.ErrNotFound
// means the movie has no trailer. Impl: *enrichpersistence.MovieVideosRepository.
type MovieTrailerReader interface {
	GetBestTrailer(ctx context.Context, movieID domain.MovieID) (enrichpersistence.MovieVideo, error)
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
	Genres     []taxonomy.Genre              // Ф2.5a localized genre chips (join order)
	Keywords   []taxonomy.Keyword            // Ф2.5a localized keyword chips (keyword_id order)
	Companies  []taxonomy.ProductionCompany  // Ф2.5b production-company sidebar (join order)
	Trailer    *enrichpersistence.MovieVideo // Ф2.5b best trailer; nil when none
	Degraded   []string                      // "movie_i18n" when no localized row for lang
}

// LibraryMembership is one active per-instance Radarr membership row.
type LibraryMembership struct {
	InstanceName  string
	RadarrMovieID int
	Monitored     bool
	HasFile       bool
	Availability  *string
	SizeOnDisk    int64
	Quality       *string
	Resolution    *int
	VideoCodec    *string
	AudioCodec    *string
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

	companies CompaniesReader    // nil-OK: Companies stays empty when unwired
	trailer   MovieTrailerReader // nil-OK: Trailer stays nil when unwired

	stale StaleMarker
	now   func() time.Time
	log   *slog.Logger

	freshener freshenerPort // nil-OK: unwired → legacy mark-stale-only behavior

	enqueuer EnrichmentEnqueuer // nil-OK: unwired → no Hot-lane enqueue (S1b)

	stubResolver MovieStubResolver // nil-OK: unwired → unknown tmdb still 404 (pre-S2)
}

// freshenerPort is the synchronous read-through seam the movie detail read
// consults BEFORE assembling the response. *MovieFreshener satisfies it; nil
// leaves the read on the pre-S1a mark-stale-only path (existing tests stay green).
type freshenerPort interface {
	EnsureFresh(ctx context.Context, canon movie.Canon, lang string) FreshenResult
}

// EnrichmentEnqueuer is the S1b async-fallback seam: when the sync freshener
// degrades (timeout/error) OR is unwired, the movie detail read pushes the
// movie onto the enrichment dispatcher's interactive Hot lane so a movie worker
// hydrates it off-request — alongside the existing MarkStaleForReenrich
// background nudge (both fire). The production impl is a wiring adapter over
// *appenrich.DispatcherImpl.Enqueue(EntityMovie, id, PriorityHot); declared
// locally so moviedetail/app never imports enrichment/app. nil-OK.
type EnrichmentEnqueuer interface {
	EnqueueMovieHot(movieID domain.MovieID)
}

// MovieStubResolver is the S2 stub-create-on-view seam. When GET /movies/:tmdbId
// misses the canon table, the usecase asks the resolver to validate the tmdb id
// against TMDB and materialise a minimal seeded stub (identity + movie_i18n) via
// the SAME discovery seed insert. It returns ports.ErrNotFound (→ 404, no row
// written) when TMDB has no such movie, so a bogus deep-link never leaves a junk
// row; any other TMDB error surfaces as-is (→ 500). The production impl is a
// wiring adapter over the runtime TMDB holder + the discovery movie stub upserter;
// declared locally so moviedetail/app never imports enrichment / discovery / tmdb.
// nil-OK: unwired keeps the pre-S2 404.
type MovieStubResolver interface {
	EnsureStub(ctx context.Context, tmdbID domain.TMDBID, lang string) error
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

// WithSidebar enables the Ф2.5b companies + best-trailer enrichment. Both readers are
// nil-OK independently (an unwired reader leaves its slice/pointer empty). Returns the
// receiver for chaining in the wiring.
func (uc *UseCase) WithSidebar(companies CompaniesReader, trailer MovieTrailerReader) *UseCase {
	uc.companies = companies
	uc.trailer = trailer
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

// WithFreshener enables the S1a synchronous read-through freshener. On a
// cold/stale movie open the freshener drives MovieWorker.HandleForced within a
// ~5s budget so the assembled Detail carries fresh ru-overview / cast / recs
// instead of a stub. nil-OK: unwired leaves the read on the mark-stale-only
// nudge. Returns the receiver for chaining in the wiring.
func (uc *UseCase) WithFreshener(f freshenerPort) *UseCase {
	uc.freshener = f
	return uc
}

// WithEnrichmentEnqueuer enables the S1b Hot-lane async fallback. On a
// stale-movie open where the sync freshener degraded (or is unwired), the async
// fallback pushes an EntityMovie/PriorityHot job so the interactive dispatcher
// lane hydrates the movie off-request within seconds — alongside the existing
// mark-stale nudge. nil-OK: unwired keeps the mark-stale-only fallback. Returns
// the receiver for chaining in the wiring.
func (uc *UseCase) WithEnrichmentEnqueuer(e EnrichmentEnqueuer) *UseCase {
	uc.enqueuer = e
	return uc
}

// WithStubResolver enables the S2 stub-create-on-view path. On a GET for a tmdb
// id absent from the canon table, the resolver validates the id against TMDB and
// inserts a minimal seeded stub (reusing the discovery seed insert) so the read
// falls through to the S1 freshener / Hot enrichment instead of 404. A bogus id
// (TMDB not-found) still 404s with no row written. nil-OK: unwired keeps the
// pre-S2 404. Returns the receiver for chaining in the wiring.
func (uc *UseCase) WithStubResolver(r MovieStubResolver) *UseCase {
	uc.stubResolver = r
	return uc
}

// Get assembles the detail for a tmdb id. ports.ErrNotFound bubbles when no
// canon row exists (→ 404). lang selects the movie_i18n row; a missing localized
// row degrades to canon fields (Degraded=["movie_i18n"]) — never an error.
//
// S2: when the canon row is absent and a stub resolver is wired, the tmdb id is
// validated against TMDB and materialised as a minimal seeded stub (reusing the
// discovery seed insert) so the read proceeds into the S1 enrichment path instead
// of 404. A bogus id (TMDB not-found) bubbles ErrNotFound with NO row written.
//
// S1a: when a sync freshener is wired, a cold/stale movie is hydrated
// synchronously (≤SyncTimeout) BEFORE assembly, and the canon is re-read so the
// hero fields reflect the fresh row. On freshener degrade (timeout/error) the
// async mark-stale fallback fires and Degraded carries "enrichment".
func (uc *UseCase) Get(ctx context.Context, tmdbID domain.TMDBID, lang string) (Detail, error) {
	canon, err := uc.canon.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		// S2 stub-create-on-view: an unknown tmdb id is materialised as a minimal
		// seeded stub (validated against TMDB) so the read proceeds into the S1
		// freshener / Hot enrichment instead of 404 — the movie analog of the series
		// resolve-or-create seam. A bogus id (TMDB not-found) bubbles ErrNotFound with
		// NO row written, preserving the 404. Unwired (stubResolver nil) or a non-
		// ErrNotFound load error keeps the pre-S2 behaviour.
		if !errors.Is(err, ports.ErrNotFound) || uc.stubResolver == nil {
			return Detail{}, err
		}
		if serr := uc.stubResolver.EnsureStub(ctx, tmdbID, lang); serr != nil {
			return Detail{}, serr // ports.ErrNotFound → 404 (no junk row); else 500
		}
		canon, err = uc.canon.GetByTMDBID(ctx, tmdbID)
		if err != nil {
			return Detail{}, err // ports.ErrNotFound bubbles (write race lost) → 404
		}
	}

	freshDegraded := false
	if uc.freshener != nil {
		res := uc.freshener.EnsureFresh(ctx, canon, lang)
		switch {
		case res.Refreshed:
			// Re-read the now-hydrated canon so hero/title/poster reflect it.
			// Fail-open: a re-read error keeps the pre-refresh canon.
			if fresh, rerr := uc.canon.GetByTMDBID(ctx, tmdbID); rerr == nil {
				canon = fresh
			}
		case res.Degraded:
			freshDegraded = true
		}
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
			Quality:       s.Quality,
			Resolution:    s.Resolution,
			VideoCodec:    s.VideoCodec,
			AudioCodec:    s.AudioCodec,
		})
	}

	uc.loadTaxonomy(ctx, &d, canon.ID, lang)
	uc.loadSidebar(ctx, &d, canon.ID)

	// Async fallback: fire the mark-stale nudge when there is NO sync freshener
	// (pre-S1a legacy behavior, preserved) OR when the sync freshener degraded
	// (timeout/error) — mirror of the series composer's degraded[] + async path.
	// (S1b upgrades maybeTriggerHydration to also enqueue EntityMovie/PriorityHot.)
	if uc.freshener == nil || freshDegraded {
		uc.maybeTriggerHydration(ctx, canon)
	}
	if freshDegraded {
		d.Degraded = append(d.Degraded, "enrichment")
	}
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

// loadSidebar fills d.Companies (join order) and d.Trailer from the movie sidebar
// tables (Ф2.5b). Companies: two reads (join ids position-ASC, then batch dict resolve
// re-projected into join order via a map — the dict reader orders by id-ASC). Trailer:
// one read; ports.ErrNotFound → no trailer (leave nil). Fail-open: every read error is
// logged at Warn and leaves the field empty — the sidebar NEVER 500s the detail. No-op
// when the readers are unwired (companies/trailer nil).
func (uc *UseCase) loadSidebar(ctx context.Context, d *Detail, movieID domain.MovieID) {
	if uc.companies != nil {
		if ids, err := uc.companies.ListByMovie(ctx, movieID); err != nil {
			uc.logTaxonomyWarn(ctx, "movie_companies", movieID, err)
		} else if len(ids) > 0 {
			rows, rerr := uc.companies.ListByIDs(ctx, ids)
			if rerr != nil {
				uc.logTaxonomyWarn(ctx, "movie_companies_dict", movieID, rerr)
			} else {
				byID := make(map[int64]taxonomy.ProductionCompany, len(rows))
				for _, c := range rows {
					byID[c.ID] = c
				}
				for _, id := range ids {
					if c, ok := byID[id]; ok {
						d.Companies = append(d.Companies, c)
					}
				}
			}
		}
	}

	if uc.trailer != nil {
		v, err := uc.trailer.GetBestTrailer(ctx, movieID)
		switch {
		case err == nil:
			vv := v
			d.Trailer = &vv
		case errors.Is(err, ports.ErrNotFound):
			// movie has no trailer — omit (not an error).
		default:
			uc.logTaxonomyWarn(ctx, "movie_videos", movieID, err)
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
// canon and, if any section is stale AND the movie has a tmdb_id, fires the
// async fallback: (1) bumps tmdb_changed_at (MarkStaleForReenrich) so the next
// throttled MovieRefreshScheduler tick re-enriches it, AND (2) — S1b — enqueues
// an EntityMovie/PriorityHot job so the interactive dispatcher lane hydrates it
// off-request within seconds. Both writers are independently nil-guarded and
// fail-open (a marker error is swallowed; Enqueue is itself non-blocking) so the
// read NEVER fails — the Radarr lesson. No-op when the movie has no tmdb_id (a
// Radarr orphan neither the picker nor the dispatcher can re-enrich) or neither
// fallback is wired.
func (uc *UseCase) maybeTriggerHydration(ctx context.Context, canon movie.Canon) {
	if canon.TMDBID == nil {
		return
	}
	if uc.stale == nil && uc.enqueuer == nil {
		return
	}
	now := time.Now
	if uc.now != nil {
		now = uc.now
	}
	if !AnyStale(MovieProbe(canon, now())) {
		return
	}
	if uc.stale != nil {
		if err := uc.stale.MarkStaleForReenrich(ctx, canon.ID, now()); err != nil && uc.log != nil {
			uc.log.WarnContext(ctx, "moviedetail.hydration.mark_stale_error",
				slog.Int64("movie_id", int64(canon.ID)),
				slog.String("error", err.Error()),
			)
		}
	}
	if uc.enqueuer != nil {
		uc.enqueuer.EnqueueMovieHot(canon.ID)
	}
}
