package enrichment

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// RefreshCast fetches /movie/{id}?language={lang} and writes the per-language
// person DISPLAY names (people_texts) for the movie's cast — the gap left by
// writeCast (HandleForced), which writes person stubs + person_credits +
// enrichment_cast_synced_at but NOT the localized names. This is the movie
// mirror of SeriesWorker.RefreshCast's people_texts block (Story 1083).
//
// ONE TMDB call: the localized cast[*].name lives in the credits payload for
// lang (GATE-ZERO F-04 proved movie /credits localizes cast names). It re-upserts
// the cast person stubs (idempotent) to resolve person_id, writes people_texts
// from the localized names (blank → skip, COALESCE-safe so a nil never wipes a
// stored value), and stamps enrichment_cast_synced_at — ALL in one Transactor tx.
//
// It does NOT rewrite person_credits (HandleForced owns cast membership). A movie
// with no tmdb id, or a worker wired without the cast-name deps, is a clean no-op.
// Driven by the ADR-0022 engine cast plugin on a movie-detail open for a non-base
// lang; idempotent + COALESCE-safe so the engine may coalesce/retry freely.
//
// Person stubs are sorted by tmdb_id ASC so concurrent movie txes lock `people`
// in a global order (mirror writeCast / SeriesWorker B-26).
func (w *MovieWorker) RefreshCast(ctx context.Context, movieID domain.MovieID, lang string) error {
	if w.deps.People == nil || w.deps.PeopleTexts == nil || w.deps.Tx == nil {
		// Cast-name drain not wired (cold-boot / opt-out tests) — no-op.
		return nil
	}
	log := w.deps.Logger.With(
		slog.String("op", "movie_refresh_cast"),
		slog.Int64("movie_id", int64(movieID)),
		slog.String("language", lang),
	)

	canon, err := w.deps.Movies.Get(ctx, movieID)
	if err != nil {
		return fmt.Errorf("movie refresh_cast: load canon %d: %w", movieID, err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.movie.refresh_cast.no_tmdb_id_skip")
		return nil
	}

	resp, err := w.deps.TMDB.GetMovie(ctx, int64(*canon.TMDBID), lang)
	if err != nil {
		return fmt.Errorf("movie refresh_cast: GetMovie(lang=%s): %w", lang, err)
	}

	// stubs carry the localized cast[*].name (movieCastStub → Name: c.Name).
	_, stubs, _ := tmdb.MapMovieCast(resp)
	slices.SortStableFunc(stubs, func(a, b people.Person) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})

	now := w.deps.Clock()
	var namesWritten int
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		personIDByTMDB := make(map[int64]int64, len(stubs))
		for _, st := range stubs {
			pid, uerr := w.deps.People.Upsert(txCtx, st)
			if uerr != nil {
				return fmt.Errorf("upsert movie cast person stub: %w", uerr)
			}
			if st.TMDBID != nil {
				personIDByTMDB[int64(*st.TMDBID)] = pid
			}
		}

		nameRows := make([]people.PersonText, 0, len(stubs))
		for _, st := range stubs {
			if st.TMDBID == nil {
				continue
			}
			pid, ok := personIDByTMDB[int64(*st.TMDBID)]
			if !ok {
				continue
			}
			// Blank/whitespace name → nil → skipped so the COALESCE upsert never
			// wipes a previously-stored value (mirror series RefreshCast).
			name := normalizePersonName(st.Name)
			if name == nil {
				continue
			}
			nameRows = append(nameRows, people.PersonText{
				PersonID: pid,
				Language: lang,
				Name:     name,
			})
		}
		if len(nameRows) > 0 {
			if berr := w.deps.PeopleTexts.BatchUpsert(txCtx, nameRows); berr != nil {
				return fmt.Errorf("batch upsert people_texts (lang=%s): %w", lang, berr)
			}
		}
		namesWritten = len(nameRows)

		// Stamp even for an empty/all-blank cast: "checked, empty" records a
		// timestamp so the engine cast plugin's recheck window gates the next open
		// (anti-storm), mirroring writeCast's unconditional MarkCastSynced.
		return w.deps.Movies.MarkCastSynced(txCtx, movieID, now)
	})
	if err != nil {
		return fmt.Errorf("movie refresh_cast: tx: %w", err)
	}

	log.InfoContext(ctx, "enrichment.movie.refresh_cast.ok",
		slog.Int("persons_upserted", len(stubs)),
		slog.Int("names_written", namesWritten))
	return nil
}
