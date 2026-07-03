package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// RefreshRecommendations fetches /tv/{tmdb_id}?language={lang} (reusing the
// existing GetTV append_to_response=recommendations slot) and writes:
//
//  1. series.UpsertStub per rec (collects canon series_id for join + texts FKs;
//     UpsertStub COALESCE preserves any existing 'full' canon hydration).
//  2. series_texts.{rec_series_id, lang}.{title=rec.Name, overview=rec.Overview}
//     — N×UPSERT side-effect per PLAN §6.3.5. THIS IS THE CRITICAL WRITE that
//     closes operator smoke symptom #3 (recs titles EN на cold reload). TMDB
//     returns already-translated Name/Overview in the requested language; we
//     persist verbatim so the next /series/{rec_id}?lang=X cold-open serves
//     the localised text without a separate /tv/{rec_id} fetch.
//  3. series_recommendations.Set(parent_id, recIDs) — DELETE+INSERT atomic
//     per parent (RecommendationsRepository.Set existing semantics). Position
//     preserved as input-slice index (0-based) — recIDs emitted in original
//     TMDB-rank order.
//  4. series.enrichment_recs_synced_at = now (MarkRecsSynced stamp).
//
// All four steps inside ONE tx. On any failure tx rollback → stamp NEVER
// written without a successful side-effect+link write.
//
// HARD RULE — ONE LANG ONLY (PLAN §4.1): exactly 1 TMDB call; series_texts
// rows only for the requested lang. Future call с different lang appends
// another series_texts row per rec (additive, not overwriting).
//
// HARD RULE — ONE PARENT ONLY: caller specifies seriesID (the parent); the
// rec children's links + per-child series_texts are touched but no rec
// child's `series_recommendations` set is recursively refreshed.
//
// force semantics (PLAN §6.-1 F-R2-5):
//   - force=true  → bypass Probe TTL gate. Always fetch + write. Used by
//     Phase 4 ChangesSyncer when TMDB's /tv/{id}/changes signals
//     {key=recommendations} — trust TMDB over local TTL.
//   - force=false → respect Probe verdict if Probe was injected. Section
//     SectionRecommendations Stale=false → early return nil. Probe nil →
//     fetch unconditionally (caller A5 EnsureFreshScope already gated).
//
// Tx shape: TMDB GetTV (out of tx) → tx{ for-each-rec-sorted-by-tmdb-id-ASC{
// UpsertStub } → for-each-rec-in-TMDB-rank-order{ SeriesTexts.Upsert } →
// Recommendations.Set → Series.MarkRecsSynced } → commit.
//
// Self-references (TMDB occasionally lists the parent in its own recommendations
// list — recursive bias) are defensively dropped from both the side-effect
// series_texts.Upsert loop AND the recIDs slice passed to Recommendations.Set
// (which has its own CHECK rejecting parent==rec; defensive double-guard).
//
// Empty TMDB response (tv.Recommendations == nil OR Results empty) → clears
// the parent's existing recommendations set (Set with empty recIDs) + stamps
// synced_at + logs empty_recommendations + returns nil. This prevents
// Probe-driven re-fire storms for series TMDB genuinely has no recommendations
// for (obscure / older catalog).
//
// canon.TMDBID nil → no-op (Sonarr-only series cannot have TMDB recommendations).
//
// No enrichment_errors journaling — narrow methods caller-orchestrated
// (A5/ChangesSyncer handle retry); Handle/HandleForced dispatcher-driven
// path retains the error-row write.
func (w *SeriesWorker) RefreshRecommendations(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
	force bool,
) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("domain", "enrichment"),
		slog.String("op", "refresh_recommendations"),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("language", lang),
		slog.Bool("force", force),
	)

	// 1. Validate lang via VO (defensive; raw string preserved for
	//    TMDB call + series_texts.language column).
	langVO, err := values.NewLanguageTag(lang)
	if err != nil {
		return fmt.Errorf("refresh_recommendations: invalid lang %q: %w", lang, err)
	}

	// 2. Read canon to get tmdb_id. Missing canon row → log + return nil
	//    (mirrors A2/A3a series_missing branch).
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.refresh_recommendations.series_missing",
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("refresh_recommendations: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.series.refresh_recommendations.no_tmdb_id_skip")
		return nil
	}

	// 3. TTL gate (force=false + Probe non-nil only). Recs section is
	//    NOT sparse-per-season — pass nil seasonNumbers per A1 contract.
	//    Fail-open per A1 Radarr lesson.
	if !force && w.deps.Probe != nil {
		verdicts, perr := w.deps.Probe.IsStale(ctx, seriesID, langVO, nil)
		if perr != nil {
			log.WarnContext(ctx, "enrichment.series.refresh_recommendations.probe_error",
				slog.String("error", perr.Error()))
		} else {
			for _, v := range verdicts {
				if v.Section == freshener.SectionRecommendations && !v.Stale {
					log.DebugContext(ctx, "enrichment.series.refresh_recommendations.skip_fresh",
						slog.String("reason", v.Reason))
					return nil
				}
			}
		}
	}

	// 4. TMDB call — ONE LANG ONLY. Reuses existing GetTV; A3b reads
	//    ONLY tv.Recommendations.Results (rest of payload discarded —
	//    parallel ship with A2 RefreshSeriesText which discards everything
	//    except Name/Overview/Tagline).
	tmdbID := int64(*canon.TMDBID)
	tv, err := w.deps.TMDB.GetTV(ctx, tmdbID, lang)
	if err != nil {
		return fmt.Errorf("refresh_recommendations: GetTV(%d, lang=%s): %w", tmdbID, lang, err)
	}

	// 5. Extract recommendations. Two parallel slices: stubs (for UpsertStub)
	//    keyed by tmdb_id-sorted order (deadlock-avoidance), and side-effect
	//    payloads keyed by tmdb_id → {Name, Overview, PosterPath, BackdropPath}
	//    for the post-UpsertStub series_texts.Upsert + UpdateRecCanonMedia loop.
	//
	//    Story 571 B-54: PosterPath + BackdropPath carried through so the
	//    tx step 6b can call RecCanonWriter.UpdateRecCanonMedia to overwrite
	//    each rec child's canon poster_asset/backdrop_asset with the TMDB
	//    lang-preferred paths (bypassing UpsertStub's COALESCE-preserve
	//    which would otherwise lock in a stale en-US path forever).
	type recSideEffect struct {
		Name         string
		Overview     string
		PosterPath   string // Story 571 B-54
		BackdropPath string // Story 571 B-54
	}
	var stubs []series.Canon
	var recOrder []domain.TMDBID // TMDB-rank order
	sideEffects := make(map[domain.TMDBID]recSideEffect)
	if tv.Recommendations != nil {
		for _, r := range tv.Recommendations.Results {
			tmdbRecID := domain.TMDBID(r.ID)
			// Defensive self-reference skip — TMDB occasionally lists the
			// parent in its own recs (recursive bias). series_recommendations
			// CHECK constraint rejects parent==rec anyway; we skip earlier
			// to avoid building stubs+side-effects for a row we'll discard.
			// Resolved-id check happens post-UpsertStub below as a second
			// defense (a stub upsert could resolve to the parent's series.id
			// via tmdb_id natural key match).
			// S-E3a — Title / PosterAsset dropped from canon; the rec stub's
			// display title + poster are written to series_texts /
			// series_media_texts via the side effects below.
			c := series.Canon{
				TMDBID:       &tmdbRecID,
				Hydration:    series.HydrationStub,
				TMDBRating:   nonZeroFloatPtr(r.VoteAverage),
				TMDBVotes:    nonZeroIntPtrSlim(r.VoteCount),
				FirstAirDate: parseDateOrNilSlim(r.FirstAirDate),
			}
			if t := parseDateOrNilSlim(r.FirstAirDate); t != nil {
				y := t.Year()
				c.Year = &y
			}
			stubs = append(stubs, c)
			recOrder = append(recOrder, tmdbRecID)
			sideEffects[tmdbRecID] = recSideEffect{
				Name:         r.Name,
				Overview:     r.Overview,
				PosterPath:   r.PosterPath,
				BackdropPath: r.BackdropPath,
			}
		}
	}

	// Sort stubs by tmdb_id ASC for global lock order (B-26 deadlock-
	// avoidance — mirrors series_worker.go:988-993 pattern). The
	// side-effect loop walks recOrder (original TMDB rank) so
	// position-in-recommendations stays TMDB-ranked.
	sortedStubs := make([]series.Canon, len(stubs))
	copy(sortedStubs, stubs)
	slices.SortStableFunc(sortedStubs, func(a, b series.Canon) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})

	now := w.deps.Clock()
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		// 6a. Upsert each rec stub (sorted-by-tmdb-id-ASC). Collect
		//     resolved series_id per rec tmdb_id for the side-effect +
		//     link loops.
		stubIDByTMDB := make(map[domain.TMDBID]domain.SeriesID, len(sortedStubs))
		for _, stub := range sortedStubs {
			id, err := w.deps.Series.UpsertStub(txCtx, stub)
			if err != nil {
				return fmt.Errorf("upsert recommendation stub: %w", err)
			}
			if stub.TMDBID != nil {
				stubIDByTMDB[*stub.TMDBID] = id
			}
		}

		// 6b. CRITICAL — N×UPSERT side-effect (§6.3.5). For each rec in
		//     original TMDB-rank order, write series_texts.{rec_id, lang}
		//     with the TMDB-localised Name + Overview. SELF-REF DEFENSIVE
		//     SKIP: if a stub upsert resolved to the parent's series_id
		//     (TMDB lists parent as its own rec), skip both the texts write
		//     AND the link emission.
		//
		//     Operator smoke symptom #3 (recs titles EN на cold reload)
		//     closes HERE. Without this loop the join row gets written but
		//     the rec's series_texts.{rec_id, lang} row stays NULL → next
		//     /series/{rec_id}?lang=X cold composer falls back to en-US.
		recIDs := make([]domain.SeriesID, 0, len(recOrder))
		for _, recTMDBID := range recOrder {
			recSeriesID, ok := stubIDByTMDB[recTMDBID]
			if !ok {
				continue
			}
			if recSeriesID == seriesID {
				continue // self-ref — drop from both side-effect AND link slice
			}
			payload, ok := sideEffects[recTMDBID]
			if !ok {
				continue
			}
			txt := series.SeriesText{
				SeriesID: recSeriesID,
				Language: lang,
				Title:    nonEmptyStringPtr(payload.Name),
				Overview: nonEmptyStringPtr(payload.Overview),
				// Tagline + EnrichedAt deliberately nil — recs side-effect
				// is NOT a canonical series_texts sync; parent stamp lives
				// on series.enrichment_recs_synced_at instead. COALESCE on
				// series_texts.enriched_at preserves any prior stamp from
				// RefreshSeriesText(rec_id) — confirmed via universal narrow-
				// writer audit (story frontmatter).
			}
			if err := w.deps.SeriesTexts.Upsert(txCtx, txt); err != nil {
				return fmt.Errorf("upsert series_texts side-effect (rec_series_id=%d): %w", recSeriesID, err)
			}

			// Story 571 B-54 — overwrite rec child's canon poster/backdrop
			// with TMDB's lang-preferred paths.
			//
			// S-A (#977) limitation: recommendation items carry only
			// root poster_path/backdrop_path (recommendations.results[*] has
			// no images[] array), so per-language poster selection is NOT
			// possible here — an extra GetTV per rec child to fetch images[]
			// is forbidden by the TMDB request budget. Rec carousels keep the
			// call-language root path; canonical per-lang art applies only to
			// the parent series' own refresh paths.
			//
			// UpdateRecCanonMedia writes the rec child's en-US
			// series_media_texts poster/backdrop (COALESCE-overwrite on a
			// non-empty path) so the rec carousel serves language-preferred
			// art on a cold visit. Nil writer preserves pre-571 behavior
			// (backwards-compat with test fixtures that don't wire
			// RecCanonWriter). Failure rolls back the entire tx so the
			// stamp is NOT written without a successful media write.
			if w.deps.RecCanonWriter != nil {
				if err := w.deps.RecCanonWriter.UpdateRecCanonMedia(txCtx, recSeriesID, payload.PosterPath, payload.BackdropPath); err != nil {
					return fmt.Errorf("update rec canon media (rec_series_id=%d): %w", recSeriesID, err)
				}
			}
			recIDs = append(recIDs, recSeriesID)
		}

		// 6c. Replace parent's recommendations join (DELETE+INSERT atomic
		//     within RecommendationsRepository.Set's inner tx — but we're
		//     already in a txCtx so the inner tx wraps gracefully).
		//     Empty recIDs clears the set — intentional behavior for the
		//     empty_recommendations branch (tv.Recommendations nil OR all
		//     recs self-referenced and got skipped).
		if err := w.deps.Recommendations.Set(txCtx, seriesID, recIDs); err != nil {
			return fmt.Errorf("set series_recommendations: %w", err)
		}

		// 6d. Stamp parent — defensive against subsequent Sonarr Upsert
		//     thanks to seriesUpsertAssignments() COALESCE on
		//     enrichment_recs_synced_at (already shipped A2).
		if err := w.deps.Series.MarkRecsSynced(txCtx, seriesID, now); err != nil {
			return fmt.Errorf("mark series recs synced: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh_recommendations: tx: %w", err)
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	if len(recOrder) == 0 {
		log.InfoContext(ctx, "enrichment.series.refresh_recommendations.empty_recommendations",
			slog.Int("duration_ms", durMs))
	} else {
		log.InfoContext(ctx, "enrichment.series.refresh_recommendations.ok",
			slog.Int("recs_count", len(recOrder)),
			slog.Int("duration_ms", durMs))
	}
	return nil
}
