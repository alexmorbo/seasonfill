package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// RefreshSeriesText fetches /tv/{tmdb_id}?language={lang} and writes
// ONLY the localised text fields (title / overview / tagline) into
// series_texts.{seriesID, lang}, then stamps
// series.enrichment_text_synced_at.
//
// HARD RULE — ONE LANG ONLY (PLAN §4.1): exactly 1 TMDB call, writes
// exactly 1 row in series_texts. No iteration over Languages.
//
// force semantics (PLAN §6.-1 F-R2-5):
//   - force=true  → bypass Probe TTL gate. Always fetch + write. Used
//     by Phase 4 ChangesSyncer when TMDB's /tv/{id}/changes signals
//     {key=overview iso_639_1=ru} — we trust TMDB over the local TTL.
//   - force=false → respect Probe verdict if Probe was injected into
//     deps. SectionOverview Stale=false → early return nil. Probe nil
//     → fetch unconditionally (caller A5 EnsureFreshScope already gated).
//
// Tx shape: TMDB GetTV (out of tx) → series_texts UPSERT (in tx) →
// MarkTextSynced UPDATE (in tx) → commit. tx rollback if either write
// fails; stamp is NEVER written without a successful series_texts UPSERT.
//
// canon.TMDBID nil → no-op (Sonarr-only series cannot be TMDB-enriched).
// Mirrors handleInternal's no_tmdb_id_skip branch.
//
// No enrichment_errors journaling — narrow methods are caller-orchestrated
// (A5/ChangesSyncer handle retry); Handle/HandleForced dispatcher-driven
// path retains the error-row write.
func (w *SeriesWorker) RefreshSeriesText(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
	force bool,
) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("domain", "enrichment"),
		slog.String("op", "refresh_series_text"),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("language", lang),
		slog.Bool("force", force),
	)

	// 1. Validate lang via VO (defensive; raw string preserved for
	//    TMDB call + series_texts.language column).
	langVO, err := values.NewLanguageTag(lang)
	if err != nil {
		return fmt.Errorf("refresh_series_text: invalid lang %q: %w", lang, err)
	}

	// 2. Read canon to get tmdb_id + (when Probe injected) feed the
	//    section verdict lookup. Missing canon row → log + return nil
	//    (same shape as handleInternal series_missing branch).
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.refresh_text.series_missing",
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("refresh_series_text: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.series.refresh_text.no_tmdb_id_skip")
		return nil
	}

	// 3. TTL gate (force=false + Probe non-nil only).
	if !force && w.deps.Probe != nil {
		verdicts, perr := w.deps.Probe.IsStale(ctx, seriesID, langVO, nil)
		if perr != nil {
			// Fail-open per A1 lesson — proceed to fetch on probe error.
			log.WarnContext(ctx, "enrichment.series.refresh_text.probe_error",
				slog.String("error", perr.Error()))
		} else {
			for _, v := range verdicts {
				if v.Section == freshener.SectionOverview && !v.Stale {
					log.DebugContext(ctx, "enrichment.series.refresh_text.skip_fresh",
						slog.String("reason", v.Reason))
					return nil
				}
			}
		}
	}

	// 4. TMDB call — ONE LANG ONLY. Re-use existing GetTV port (we
	//    discard everything except .Name / .Overview / .Tagline; the
	//    TMDB call carries them in one round-trip alongside append_to_response
	//    payloads we ignore). Could be /tv/{id}?language=X&append_to_response=
	//    "" optimised in future; out of scope here.
	tv, err := w.deps.TMDB.GetTV(ctx, int64(*canon.TMDBID), lang)
	if err != nil {
		return fmt.Errorf("refresh_series_text: GetTV(lang=%s): %w", lang, err)
	}

	// 5. Map → series.SeriesText (title/overview/tagline only).
	text := series.SeriesText{
		SeriesID: seriesID,
		Language: lang,
		Title:    nonEmptyStringPtr(tv.Name),
		Overview: nonEmptyStringPtr(tv.Overview),
		Tagline:  nonEmptyStringPtr(tv.Tagline),
	}

	// 5b. Per-language poster/backdrop (C-posters-A, Story 584a). The
	//     SAME GetTV(lang) payload carries tv.PosterPath / tv.BackdropPath
	//     (identical struct A4 reads) — zero extra TMDB round-trips.
	//     Eager-resolve the default-size hashes OUTSIDE the tx: Resolve
	//     writes a media_assets pending row (EnsurePending) + enqueues the
	//     download, so the per-lang asset pre-warms into the pipeline
	//     instead of only fetching on a user's first read. media_assets is
	//     a different natural key — nesting the Resolve DB write inside the
	//     series_texts tx risks cross-table lock contention (A4 lesson,
	//     series_worker_refresh_media_assets.go:45-53). nil resolver → hash
	//     stays nil (asset path still stored + read paths re-derive).
	var posterHash, backdropHash *string
	posterPath := nonEmptyStringPtr(tv.PosterPath)
	backdropPath := nonEmptyStringPtr(tv.BackdropPath)
	if w.deps.MediaResolver != nil {
		if posterPath != nil {
			posterHash = w.deps.MediaResolver.Resolve(ctx, posterPath, "w342", "poster_w342")
		}
		if backdropPath != nil {
			backdropHash = w.deps.MediaResolver.Resolve(ctx, backdropPath, "w1280", "backdrop_w1280")
		}
	}

	// 6. Atomic write — series_texts UPSERT + (nil-OK) series_media_texts
	//    UPSERT + stamp UPDATE in one tx. Stamp NEVER written without a
	//    successful series_texts UPSERT.
	now := w.deps.Clock()
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		if err := w.deps.SeriesTexts.Upsert(txCtx, text); err != nil {
			return fmt.Errorf("upsert series_texts: %w", err)
		}
		if w.deps.SeriesMediaTexts != nil {
			media := series.SeriesMediaText{
				SeriesID:      seriesID,
				Language:      lang,
				PosterAsset:   posterPath,
				PosterHash:    posterHash,
				BackdropAsset: backdropPath,
				BackdropHash:  backdropHash,
				EnrichedAt:    &now,
			}
			if err := w.deps.SeriesMediaTexts.Upsert(txCtx, media); err != nil {
				return fmt.Errorf("upsert series_media_texts: %w", err)
			}
		}
		if err := w.deps.Series.MarkTextSynced(txCtx, seriesID, now); err != nil {
			return fmt.Errorf("mark text synced: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh_series_text: tx: %w", err)
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	log.InfoContext(ctx, "enrichment.series.refresh_text.ok",
		slog.Bool("poster_present", posterPath != nil),
		slog.Bool("backdrop_present", backdropPath != nil),
		slog.Int("duration_ms", durMs))
	return nil
}
