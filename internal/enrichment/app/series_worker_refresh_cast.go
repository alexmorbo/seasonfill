package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// RefreshCast fetches /tv/{tmdb_id}?language={lang} (with append_to_response
// = aggregate_credits + …) and writes series-level cast/crew:
//   - people stubs (one row per credit, idempotent on tmdb_id)
//   - person_credits (media_type='tv', tmdb_media_id=canon.tmdb_id) —
//     re-walks tv.AggregateCredits via the same resolver that
//     applyAllForLanguage step 7 uses.
//
// Then stamps series.enrichment_cast_synced_at.
//
// NOT touched here:
//   - episode-level credits (step 7b — caller A5 will dispatch
//     RefreshSeasonSlim for those when needed)
//   - canonical series fields (RefreshSeriesText covers text;
//     A3b/A4 cover recs/media)
//
// HARD RULE — ONE LANG ONLY (PLAN §4.1): exactly 1 TMDB call. The
// localisation is carried via TMDB's `?language=` parameter which
// returns per-character translations inside the credits payload. The
// language-neutral base credit row lives in person_credits (last-lang-wins
// on character_name); the per-language character name is written to the
// person_credits_texts side-table keyed by (person_credit_id, lang) so the
// reader can resolve requested-lang → en-US → base (S-G).
//
// force semantics — same as RefreshSeriesText (F-R2-5).
//
// Probe section consulted: SectionCast.
func (w *SeriesWorker) RefreshCast(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
	force bool,
) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("op", "refresh_cast"),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("language", lang),
		slog.Bool("force", force),
	)

	langVO, err := values.NewLanguageTag(lang)
	if err != nil {
		return fmt.Errorf("refresh_cast: invalid lang %q: %w", lang, err)
	}

	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.refresh_cast.series_missing",
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("refresh_cast: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.series.refresh_cast.no_tmdb_id_skip")
		return nil
	}

	// TTL gate.
	if !force && w.deps.Probe != nil {
		verdicts, perr := w.deps.Probe.IsStale(ctx, seriesID, langVO, nil)
		if perr != nil {
			log.WarnContext(ctx, "enrichment.series.refresh_cast.probe_error",
				slog.String("error", perr.Error()))
		} else {
			for _, v := range verdicts {
				if v.Section == freshener.SectionCast && !v.Stale {
					log.DebugContext(ctx, "enrichment.series.refresh_cast.skip_fresh",
						slog.String("reason", v.Reason))
					return nil
				}
			}
		}
	}

	tv, err := w.deps.TMDB.GetTV(ctx, int64(*canon.TMDBID), lang)
	if err != nil {
		return fmt.Errorf("refresh_cast: GetTV(lang=%s): %w", lang, err)
	}

	// Map cast — re-use existing tmdb mapper. credits carries the
	// per-credit metadata, stubs the deduped people.Person rows. We
	// IGNORE credits.SeriesID (will be wired in tx) and re-walk tv via
	// resolveSeriesCreditsWithPersonID to attach the person FK after
	// the stub UPSERTs settle (same dance applyAllForLanguage step 7
	// performs — see series_worker.go:851).
	_, stubs := tmdb.MapTVToCredits(tv)
	// Deterministic stub order for cross-tx deadlock avoidance (B-26
	// pattern, mirrored from series_worker.go:820).
	slices.SortStableFunc(stubs, func(a, b people.Person) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})

	now := w.deps.Clock()
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		personIDByTMDB := make(map[int]int64, len(stubs))
		for _, st := range stubs {
			pid, err := w.deps.People.Upsert(txCtx, st)
			if err != nil {
				return fmt.Errorf("upsert person stub: %w", err)
			}
			if st.TMDBID != nil {
				personIDByTMDB[int(*st.TMDBID)] = pid
			}
		}

		finalCredits, dropped := resolveSeriesCreditsWithPersonID(tv, seriesID, personIDByTMDB)
		if len(finalCredits) > 0 {
			pcRows := mapSeriesCreditsToPersonCredits(finalCredits, tv, int64(*canon.TMDBID))
			ids, err := w.deps.PersonCredits.BatchUpsert(txCtx, pcRows)
			if err != nil {
				return fmt.Errorf("batch upsert person_credits (tv): %w", err)
			}
			// S-G — write per-language cast character names keyed by the
			// person_credits surrogate id. ids[i] ↔ pcRows[i] (BatchUpsert
			// returns ids in input order). Only rows that actually carry a
			// non-empty character name produce a texts row; an empty/nil
			// name is normalized to nil so the COALESCE upsert never wipes a
			// previously-stored value. nil-OK: skip entirely when the port
			// is unwired (cold-boot / tests).
			if w.deps.PersonCreditsTexts != nil && len(ids) == len(pcRows) {
				textRows := make([]people.PersonCreditText, 0, len(pcRows))
				for i, row := range pcRows {
					name := normalizeCharacterName(row.CharacterName)
					if name == nil {
						continue
					}
					textRows = append(textRows, people.PersonCreditText{
						PersonCreditID: ids[i],
						Language:       lang,
						CharacterName:  name,
					})
				}
				if len(textRows) > 0 {
					if err := w.deps.PersonCreditsTexts.BatchUpsert(txCtx, textRows); err != nil {
						return fmt.Errorf("batch upsert person_credits_texts (lang=%s): %w", lang, err)
					}
				}
			}
		}
		if dropped > 0 {
			log.WarnContext(txCtx, "enrichment.series.refresh_cast.credits_dropped",
				slog.Int("dropped", dropped),
				slog.Int("kept", len(finalCredits)))
		}

		if err := w.deps.Series.MarkCastSynced(txCtx, seriesID, now); err != nil {
			return fmt.Errorf("mark cast synced: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh_cast: tx: %w", err)
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	log.InfoContext(ctx, "enrichment.series.refresh_cast.ok",
		slog.Int("persons_upserted", len(stubs)),
		slog.Int("duration_ms", durMs))
	return nil
}

// normalizeCharacterName trims a credit character name and maps the empty
// string to nil so the person_credits_texts COALESCE upsert treats "no
// character" as "leave existing untouched" rather than overwriting with "".
func normalizeCharacterName(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
