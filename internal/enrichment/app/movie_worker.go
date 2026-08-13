// movie_worker.go — Ф6-R-4a (L3-2) movie TMDB hydration worker.
//
// MovieWorker.HandleForced hydrates ONE canon movie row from /movie/{id}:
// load canon → GetMovie(language-aware) → map → COALESCE Upsert → localized
// movie_i18n write → MarkTMDBSynced. Far simpler than SeriesWorker.HandleForced
// (no seasons/episodes/people/taxonomy fanout) — a movie is a single canon row
// plus one localized side-table row.
//
// This worker does NOT import discovery and consumes only narrow ports
// (movie_ports.go). It is driven by the SEPARATE MovieRefreshScheduler (its own
// budget/ticker) so movie hydration never dequeues series worker budget.
package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieWorkerDeps is the construction surface. TMDB + Movies are required;
// I18n / Resolver are nil-OK; BaseLang / Logger / Clock default.
type MovieWorkerDeps struct {
	TMDB     MovieTMDBClient
	Movies   MovieCanonRepo
	I18n     MovieI18nWriter // nil-OK
	Resolver MediaResolver   // nil-OK (reuses the series A4 resolver port)
	// OMDb — Ф6-R-4a (L3-3) post-hydrate OMDb ratings trigger. nil-OK (OMDb
	// disabled at boot). When set AND the hydrated movie carries an imdb_id, the
	// worker fires it right after MarkTMDBSynced (enqueue-after-imdb): OMDb only
	// runs once TMDB has stamped the imdb_id. Best-effort — an OMDb failure is
	// logged, never failing or rolling back the committed TMDB hydrate.
	OMDb MovieOMDbHandler
	// Collections — Ф6-R-5 fail-soft collection populate seam. nil-OK (disabled at
	// boot = exact pre-R-5 behavior). When set AND the hydrated canon carries a
	// non-nil CollectionID, the worker fires it AFTER MarkTMDBSynced. Best-effort:
	// a failure is logged, never failing/rolling back the committed TMDB hydrate.
	Collections MovieCollectionPopulator
	// People / PersonCredits / Tx — Ф1.1a movie CAST writer seam. All THREE must be
	// set for the cast write to run (nil-OK: when any is nil the worker skips cast,
	// exact pre-Ф1.1a behavior, so existing MovieWorker construction/tests are
	// unaffected). People upserts cast person stubs (→ person_id); PersonCredits
	// writes the movie person_credits rows via BatchUpsertAuthoritative; Tx makes
	// the stubs + credits + enrichment_cast_synced_at stamp atomic.
	People        PeopleRepo        // nil-OK
	PersonCredits PersonCreditsPort // nil-OK
	Tx            Transactor        // nil-OK
	// Genres / Keywords / Companies — Ф1.1b movie taxonomy trio writers. Each nil-OK:
	// when nil the worker skips that writer (exact pre-Ф1.1b behavior, so existing
	// movie_worker_test.go fixtures — which set none — stay green). Gated together with Tx
	// in HandleForced; each writer is its own Transactor tx. Production impls compose the
	// enrichment taxonomy repos (see wiring/enrichment_movie.go).
	Genres    MovieGenresWriter    // nil-OK
	Keywords  MovieKeywordsWriter  // nil-OK
	Companies MovieCompaniesWriter // nil-OK
	// Videos / Recs — Ф1.1c movie media + recommendations writers. Each nil-OK: when nil the
	// worker skips that writer (exact pre-Ф1.1c behavior, so existing movie_worker_test.go
	// fixtures stay green). Gated with Tx + the decoded sub-resource in HandleForced; each is
	// its own Transactor tx. Recs is the F-Ф1-12 stub-before-join FK path.
	Videos   MovieVideosWriter // nil-OK
	Recs     MovieRecsWriter   // nil-OK
	BaseLang string            // default tmdb.DefaultLanguage
	Logger   *slog.Logger
	Clock    func() time.Time
}

// MovieOMDbHandler is the post-hydrate OMDb trigger seam. Production impl is
// *MovieOMDbWorker.HandleMovieOMDb. Kept a narrow one-method port so the TMDB
// hydration worker never imports the OMDb client — the wiring layer binds the
// concrete worker.
type MovieOMDbHandler interface {
	HandleMovieOMDb(ctx context.Context, movieID int64) error
}

// MovieWorker hydrates canon movies from TMDB. Satisfies MovieForceRefresher.
type MovieWorker struct {
	deps     MovieWorkerDeps
	baseLang string
}

// NewMovieWorker validates required deps and applies defaults. Returns an error
// (rather than panicking) so the boot wirer can log "movie worker disabled".
func NewMovieWorker(deps MovieWorkerDeps) (*MovieWorker, error) {
	if deps.TMDB == nil {
		return nil, errors.New("movie worker: TMDB is required")
	}
	if deps.Movies == nil {
		return nil, errors.New("movie worker: Movies is required")
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	baseLang := deps.BaseLang
	if baseLang == "" {
		baseLang = tmdb.DefaultLanguage
	}
	return &MovieWorker{deps: deps, baseLang: baseLang}, nil
}

// HandleForced hydrates the canon movie identified by movieID. Idempotent: each
// write (canon Upsert, movie_i18n Upsert, MarkTMDBSynced) is independently
// COALESCE-safe, so a partial failure just leaves the row stale for the next
// tick to retry (self-healing; no cross-write transaction needed for R-4a).
func (w *MovieWorker) HandleForced(ctx context.Context, movieID int64) error {
	canon, err := w.deps.Movies.Get(ctx, domain.MovieID(movieID))
	if err != nil {
		return fmt.Errorf("movie worker: load canon %d: %w", movieID, err)
	}
	if canon.TMDBID == nil {
		// A tmdb-less movie (Radarr orphan) has nothing to fetch. Skip cleanly —
		// mirror the series worker's nil-tmdb skip. Not an error.
		w.deps.Logger.DebugContext(ctx, "enrichment.movie.skipped",
			slog.Int64("movie_id", movieID),
			slog.String("reason", "no_tmdb_id"),
		)
		return nil
	}
	tmdbID := int64(*canon.TMDBID)

	// language-aware detail fetch (#1184 guard — GetMovie calls c.languageFor).
	resp, err := w.deps.TMDB.GetMovie(ctx, tmdbID, w.baseLang)
	if err != nil {
		return fmt.Errorf("movie worker: GetMovie(%d): %w", tmdbID, err)
	}

	mapped := tmdb.MapMovieToCanon(resp)
	mapped.ID = canon.ID // target the existing row by PK (COALESCE Upsert path)

	// Media pre-warm side-effect (nil-OK): mint media_assets pending rows so the
	// eventual read has stable sha256 handles. Canon keeps RAW TMDB paths (mirror
	// of the discovery stub + series A4 "canon carries raw paths" choice).
	if w.deps.Resolver != nil {
		if mapped.PosterAsset != nil {
			_ = w.deps.Resolver.Resolve(ctx, mapped.PosterAsset, "w342", "poster_w342")
		}
		if mapped.BackdropAsset != nil {
			_ = w.deps.Resolver.Resolve(ctx, mapped.BackdropAsset, "w1280", "backdrop_w1280")
		}
	}

	if _, err := w.deps.Movies.Upsert(ctx, mapped); err != nil {
		return fmt.Errorf("movie worker: upsert canon %d: %w", canon.ID, err)
	}

	now := w.deps.Clock()

	// Localized rows — one per supported UI language (mirror of PersonWorker's
	// per-language biography fan-out, person_worker.go:201). The base language
	// uses the response root (resp.Title/Overview/Tagline, already localized to
	// w.baseLang); every OTHER language is pulled from resp.Translations
	// (append_to_response=translations), so ALL supported languages are populated
	// from this SINGLE GetMovie fetch — no extra per-language TMDB round-trips.
	// The canon poster/backdrop are language-independent for movies (no per-lang
	// image path on this route), so every row carries them — keeping the reader's
	// poster-non-empty ladder able to select a localized row (S-E2/E3 invariant #2).
	if w.deps.I18n != nil {
		trByLang := movieTranslationsByLang(resp)
		baseShort := shortLang(w.baseLang)
		for _, lang := range locale.SupportedUserLanguages {
			title, overview, tagline := resp.Title, resp.Overview, resp.Tagline
			if shortLang(lang) != baseShort {
				tr, ok := trByLang[shortLang(lang)]
				if !ok {
					// TMDB has no translation for this language — skip so the
					// COALESCE writer never creates an all-empty row.
					continue
				}
				title, overview, tagline = tr.Name, tr.Overview, tr.Tagline
			}
			if err := w.deps.I18n.UpsertEnriched(ctx, canon.ID, lang,
				title, overview, tagline,
				mapped.PosterAsset, mapped.BackdropAsset, now); err != nil {
				return fmt.Errorf("movie worker: upsert i18n %d (%s): %w", canon.ID, lang, err)
			}
		}
	}

	if err := w.deps.Movies.MarkTMDBSynced(ctx, canon.ID, now); err != nil {
		return fmt.Errorf("movie worker: mark synced %d: %w", canon.ID, err)
	}

	w.deps.Logger.InfoContext(ctx, "enrichment.movie.hydrated",
		slog.Int64("movie_id", int64(canon.ID)),
		slog.Int64("tmdb_id", tmdbID),
	)

	// Ф1.1a: movie CAST → person_credits (media_type='movie'), wrapped in the
	// Transactor (the sole transactional path in this worker). Gated on all three
	// cast deps + a decoded credits sub-resource. Returns on failure so the
	// scheduler retries — the canon hydrate above already committed and is
	// idempotent, so a retry re-lands the cast without duplicating canon work.
	if w.deps.People != nil && w.deps.PersonCredits != nil && w.deps.Tx != nil && resp.Credits != nil {
		if err := w.writeCast(ctx, canon.ID, resp); err != nil {
			return fmt.Errorf("movie worker: write cast %d: %w", canon.ID, err)
		}
	}

	// Ф6-R-4a L3-3: enqueue-after-imdb OMDb ratings follow-up. Runs inline on the
	// movie scheduler goroutine (isolated from the series worker/dispatcher budget
	// — the movie mirror of the EntityOMDb precedent). Gated on a present imdb_id
	// (mapped from this fetch, or preserved on the canon). Best-effort: a failure
	// is logged only and must not fail the already-committed TMDB hydrate.
	if w.deps.OMDb != nil && (mapped.IMDBID != nil || canon.IMDBID != nil) {
		if oerr := w.deps.OMDb.HandleMovieOMDb(ctx, int64(canon.ID)); oerr != nil {
			w.deps.Logger.WarnContext(ctx, "enrichment.movie.omdb_followup_failed",
				slog.Int64("movie_id", int64(canon.ID)),
				slog.String("error", oerr.Error()),
			)
		}
	}

	// Ф6-R-5: fail-soft collection populate. Runs AFTER the movie hydrate is
	// committed (canon Upsert + MarkTMDBSynced). Gated on a configured populator
	// AND a non-nil CollectionID on the freshly hydrated canon. Best-effort — a
	// failure is logged only and MUST NOT fail the already-committed movie
	// hydrate (mirror of the OMDb follow-up policy above).
	if w.deps.Collections != nil && mapped.CollectionID != nil {
		if cerr := w.deps.Collections.PopulateCollection(ctx, *mapped.CollectionID); cerr != nil {
			w.deps.Logger.WarnContext(ctx, "enrichment.movie.collection_populate_failed",
				slog.Int64("movie_id", int64(canon.ID)),
				slog.Int("collection_id", *mapped.CollectionID),
				slog.String("error", cerr.Error()),
			)
		}
	}

	// Ф1.1b: movie taxonomy trio (genres / keywords / companies) into the movie_* join
	// tables. Each writer is an independent Transactor tx (parent dict seed by tmdb_id +
	// base-lang i18n seed + DELETE+INSERT join; keywords also stamps
	// enrichment_keywords_synced_at). Each is nil-OK gated (Tx + its writer port). Genres
	// and companies are /movie root fields (always decoded); keywords additionally gates on
	// the decoded sub-resource. SetMovie is authoritative (clears removed items on refresh).
	// Errors return so the scheduler retries — the canon hydrate above already committed and
	// every write is idempotent.
	if w.deps.Tx != nil && w.deps.Genres != nil {
		if err := w.writeGenres(ctx, canon.ID, tmdb.MapMovieGenres(resp)); err != nil {
			return fmt.Errorf("movie worker: write genres %d: %w", canon.ID, err)
		}
	}
	if w.deps.Tx != nil && w.deps.Keywords != nil && resp.Keywords != nil {
		if err := w.writeKeywords(ctx, canon.ID, tmdb.MapMovieKeywords(resp)); err != nil {
			return fmt.Errorf("movie worker: write keywords %d: %w", canon.ID, err)
		}
	}
	if w.deps.Tx != nil && w.deps.Companies != nil {
		if err := w.writeCompanies(ctx, canon.ID, tmdb.MapMovieCompanies(resp)); err != nil {
			return fmt.Errorf("movie worker: write companies %d: %w", canon.ID, err)
		}
	}

	// Ф1.1c: best trailer → movie_videos (authoritative single-row replace) + media stamp.
	// Gated on Tx + the writer + a decoded videos sub-resource. A nil chosen trailer still runs
	// (clears the rows + stamps — the media section was checked). Own Transactor tx.
	if w.deps.Tx != nil && w.deps.Videos != nil && resp.Videos != nil {
		if err := w.writeVideos(ctx, canon.ID, tmdb.MapMovieBestTrailer(resp)); err != nil {
			return fmt.Errorf("movie worker: write videos %d: %w", canon.ID, err)
		}
	}
	// Ф1.1c: recommendations → movie_recommendations (F-Ф1-12 stub-before-join). Gated on Tx +
	// the writer + a decoded recommendations sub-resource. Own Transactor tx (stub upserts +
	// join Set + recs stamp, atomic). Errors return so the scheduler retries — the canon hydrate
	// above already committed and every write is idempotent.
	if w.deps.Tx != nil && w.deps.Recs != nil && resp.Recommendations != nil {
		if err := w.writeRecommendations(ctx, canon.ID, resp); err != nil {
			return fmt.Errorf("movie worker: write recommendations %d: %w", canon.ID, err)
		}
	}
	return nil
}

// writeCast upserts the movie's cast person stubs, resolves each credit's person_id,
// writes the person_credits rows authoritatively, and stamps
// enrichment_cast_synced_at — ALL inside one Transactor tx (Ф1.1a). Person stubs are
// sorted by tmdb_id ASC so concurrent movie txes lock `people` in a global order
// (mirror SeriesWorker B-26). A no-cast movie (or one whose every stub upsert was
// suppressed) still stamps the cast clock ("checked, empty") — a stamp-only tx —
// so the Ф1.2 on-read hydration probe stops re-firing the picker forever (mirror
// writeRecommendations "prevents re-fire storms"). The authoritative person_credits
// write only runs when there are resolvable rows, so a transient empty TMDB credits
// response stamps without nuking existing cast.
func (w *MovieWorker) writeCast(ctx context.Context, movieID domain.MovieID, resp *tmdb.MovieResponse) error {
	credits, stubs, tmdbPersonIDs := tmdb.MapMovieCast(resp)
	slices.SortStableFunc(stubs, func(a, b people.Person) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})
	return w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		personIDByTMDB := make(map[int64]int64, len(stubs))
		for _, st := range stubs {
			pid, err := w.deps.People.Upsert(txCtx, st)
			if err != nil {
				return fmt.Errorf("upsert movie cast person stub: %w", err)
			}
			if st.TMDBID != nil {
				personIDByTMDB[int64(*st.TMDBID)] = pid
			}
		}
		rows := make([]people.PersonCredit, 0, len(credits))
		for i := range credits {
			pid, ok := personIDByTMDB[tmdbPersonIDs[i]]
			if !ok || pid == 0 {
				continue // stub upsert suppressed this person — drop its credit.
			}
			credits[i].PersonID = pid
			rows = append(rows, credits[i])
		}
		if len(rows) > 0 {
			if _, err := w.deps.PersonCredits.BatchUpsertAuthoritative(txCtx, rows); err != nil {
				return fmt.Errorf("batch upsert person_credits (movie): %w", err)
			}
		}
		// Stamp even for an empty cast / all-suppressed stubs: "checked, empty"
		// records a timestamp so the on-read probe stops re-firing (mirror
		// writeRecommendations/writeKeywords). Empty rows → stamp-only tx; the
		// authoritative write above is skipped so a transient empty credits
		// response never clears an existing cast.
		return w.deps.Movies.MarkCastSynced(txCtx, movieID, w.deps.Clock())
	})
}

// movieTranslationsByLang indexes append_to_response=translations by bare
// 2-letter language code (shortLang) → localized text fields. Mirrors the
// PersonWorker translation index (person_worker.go:192-198). Empty map when the
// movie carries no translations sub-resource.
func movieTranslationsByLang(resp *tmdb.MovieResponse) map[string]tmdb.TVTranslationData {
	out := map[string]tmdb.TVTranslationData{}
	if resp == nil || resp.Translations == nil {
		return out
	}
	for i := range resp.Translations.Translations {
		t := &resp.Translations.Translations[i]
		out[shortLang(t.ISO6391)] = t.Data
	}
	return out
}
