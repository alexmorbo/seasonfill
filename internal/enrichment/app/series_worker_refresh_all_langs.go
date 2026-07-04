package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// RefreshSeriesAllLangs fetches /tv/{tmdb_id} ONCE (base-lang root +
// translations sub-resource + union include_image_language) and writes
// series_texts + series_media_texts for EVERY supported user language in a
// single transaction, then stamps series.enrichment_text_synced_at.
//
// It generalises RefreshSeriesText (one-lang) to all-langs: it halves the
// TMDB GetTV volume for the SectionOverview freshener path, removes the
// first-view localisation lag (both languages land on the first cold open),
// and guarantees the en-US base row (the foundation S-E depends on).
//
// Base-lang guarantee & non-base skip (S-B design decision D2):
//   - The base language (locale.Default() = en-US) row is ALWAYS written.
//     GetTVAllLangs pins language=en-US, so the ROOT Name/Overview/Tagline
//     ARE the en-US source; per-field fallback to root yields a complete base
//     row even when translations[] is empty.
//   - A non-base language row is written ONLY when translations[] carries a
//     matching iso_639_1 entry. Within that entry each empty field falls back
//     to the root value (TMDB frequently ships an empty data.overview). When
//     NO entry exists the language is SKIPPED entirely — no row is written,
//     so the probe's missing_lang verdict stays Stale and the per-lang async
//     path can still fill it later. Writing a root-filled (English) row under
//     a ru-RU key would both mislabel the text AND mask the probe.
//
// force / probe-gate / no-tmdb-id / series-missing semantics mirror
// RefreshSeriesText exactly (the probe IsStale call uses the base-lang VO).
// Tx shape: GetTVAllLangs (out of tx) → N×series_texts UPSERT +
// N×series_media_texts UPSERT (in tx) → ONE MarkTextSynced UPDATE (in tx) →
// commit. Rollback drops the stamp; the stamp is NEVER written without a
// successful series_texts UPSERT.
func (w *SeriesWorker) RefreshSeriesAllLangs(
	ctx context.Context,
	seriesID domain.SeriesID,
	force bool,
) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("op", "refresh_series_all_langs"),
		slog.Int64("entity_id", int64(seriesID)),
		slog.Bool("force", force),
	)

	// 1. Read canon. Missing row → log + return nil (handleInternal
	//    series_missing shape).
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.refresh_all_langs.series_missing",
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("refresh_series_all_langs: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.series.refresh_all_langs.no_tmdb_id_skip")
		return nil
	}

	// 2. TTL gate (force=false + Probe non-nil). Probe the base language's
	//    overview section; fresh → early return. Base-lang VO is always valid;
	//    if VO construction ever fails we fail-open (skip the gate) rather than
	//    error the whole refresh.
	if !force && w.deps.Probe != nil {
		if baseVO, verr := values.NewLanguageTag(locale.Default()); verr == nil {
			verdicts, perr := w.deps.Probe.IsStale(ctx, seriesID, baseVO, nil)
			if perr != nil {
				// Fail-open per A1 lesson — proceed to fetch on probe error.
				log.WarnContext(ctx, "enrichment.series.refresh_all_langs.probe_error",
					slog.String("error", perr.Error()))
			} else {
				for _, v := range verdicts {
					if v.Section == freshener.SectionOverview && !v.Stale {
						log.DebugContext(ctx, "enrichment.series.refresh_all_langs.skip_fresh",
							slog.String("reason", v.Reason))
						return nil
					}
				}
			}
		}
	}

	// 3. ONE TMDB call: base-lang root + translations + union images.
	tv, err := w.deps.TMDB.GetTVAllLangs(ctx, int64(*canon.TMDBID))
	if err != nil {
		return fmt.Errorf("refresh_series_all_langs: GetTVAllLangs: %w", err)
	}

	// 4. Index translations by short iso code (nil-safe).
	trByLang := make(map[string]*tmdb.TVTranslation)
	if tv.Translations != nil {
		for i := range tv.Translations.Translations {
			t := &tv.Translations.Translations[i]
			trByLang[shortLang(t.ISO6391)] = t
		}
	}

	base := locale.Default()
	now := w.deps.Clock()

	// 5. Build per-lang writes OUTSIDE the tx. Eager MediaResolver.Resolve
	//    mints the media_assets pending row + enqueues the download; nesting
	//    that inside the series_texts tx risks cross-table lock contention
	//    (refresh_text.go pattern).
	type pendingWrite struct {
		text  series.SeriesText
		media *series.SeriesMediaText // nil when the SeriesMediaTexts port is nil
	}
	writes := make([]pendingWrite, 0, len(locale.SupportedUserLanguages))

	for _, lang := range locale.SupportedUserLanguages {
		tr := trByLang[shortLang(lang)]
		if lang != base && tr == nil {
			// Non-base lang with no translation entry → skip (D2). Keeps the
			// row absent so the probe's missing_lang verdict stays Stale.
			continue
		}

		// Per-FIELD root fallback. For the base lang tr may be nil or the en
		// entry — either way root (en-US) is the correct fallback source.
		name, overview, tagline := tv.Name, tv.Overview, tv.Tagline
		if tr != nil {
			if tr.Data.Name != "" {
				name = tr.Data.Name
			}
			if tr.Data.Overview != "" {
				overview = tr.Data.Overview
			}
			if tr.Data.Tagline != "" {
				tagline = tr.Data.Tagline
			}
		}
		text := series.SeriesText{
			SeriesID: seriesID,
			Language: lang,
			Title:    nonEmptyStringPtr(name),
			Overview: nonEmptyStringPtr(overview),
			Tagline:  nonEmptyStringPtr(tagline),
		}

		var media *series.SeriesMediaText
		if w.deps.SeriesMediaTexts != nil {
			// S-A pick with root fallback (reuses pickPosterForLang /
			// pickBackdropForLang from series_worker_images.go).
			posterPath := pickPosterForLang(tv.Images, lang)
			if posterPath == nil {
				posterPath = nonEmptyStringPtr(tv.PosterPath)
			}
			backdropPath := pickBackdropForLang(tv.Images, lang)
			if backdropPath == nil {
				backdropPath = nonEmptyStringPtr(tv.BackdropPath)
			}
			var posterHash, backdropHash *string
			if w.deps.MediaResolver != nil {
				if posterPath != nil {
					posterHash = w.deps.MediaResolver.Resolve(ctx, posterPath, "w342", "poster_w342")
				}
				if backdropPath != nil {
					backdropHash = w.deps.MediaResolver.Resolve(ctx, backdropPath, "w1280", "backdrop_w1280")
				}
			}
			media = &series.SeriesMediaText{
				SeriesID:      seriesID,
				Language:      lang,
				PosterAsset:   posterPath,
				PosterHash:    posterHash,
				BackdropAsset: backdropPath,
				BackdropHash:  backdropHash,
				EnrichedAt:    &now,
			}
		}
		writes = append(writes, pendingWrite{text: text, media: media})
	}

	// 6. Atomic write — N×series_texts + (nil-OK) N×series_media_texts + ONE
	//    stamp in one tx. writes always contains the base row, so the stamp is
	//    always backed by ≥1 successful UPSERT.
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		for _, wr := range writes {
			if err := w.deps.SeriesTexts.Upsert(txCtx, wr.text); err != nil {
				return fmt.Errorf("upsert series_texts (lang=%s): %w", wr.text.Language, err)
			}
			if wr.media != nil {
				if err := w.deps.SeriesMediaTexts.Upsert(txCtx, *wr.media); err != nil {
					return fmt.Errorf("upsert series_media_texts (lang=%s): %w", wr.media.Language, err)
				}
			}
		}
		if err := w.deps.Series.MarkTextSynced(txCtx, seriesID, now); err != nil {
			return fmt.Errorf("mark text synced: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh_series_all_langs: tx: %w", err)
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	log.InfoContext(ctx, "enrichment.series.refresh_all_langs.ok",
		slog.Int("langs", len(writes)),
		slog.Int("duration_ms", durMs))
	return nil
}
