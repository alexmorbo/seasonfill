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
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// RefreshMediaAssets fetches /tv/{tmdb_id} ONCE via GetTVAllLangs (base-lang
// root + union include_image_language, lang-agnostic — matching SectionMedia's
// lang-agnostic probe semantics) and writes the per-language art side-tables:
//
//  1. series_media_texts — one poster/backdrop row per supported user language.
//     - BASE lang (locale.Default() = en-US): pickPosterForLang(tv.Images) with
//     root fallback tv.PosterPath; pickBackdropForLang with root fallback
//     tv.BackdropPath. This guarantees the en-US baseline row and closes the
//     "no media row while SectionMedia is Fresh" silent hole.
//     - NON-base langs (ru-RU): poster uses pickPosterForLangStrict — EXACT
//     short(lang) tier ONLY, no agnostic/en/root fallback, so a ru poster row
//     is never poisoned by en art. Backdrop uses pickBackdropForLang (short(lang)
//     with lang-agnostic root fallback), since backdrops are typically textless
//     and a language-neutral backdrop is acceptable.
//     - Story 1081a: a row is ALWAYS written, even when poster and backdrop are
//     both nil — an absence row (asset NULL + *_checked_at = now) is the
//     confirmed-absent PRESENCE marker the skeleton hero reads to serve the
//     stable original poster instead of re-showing a stale localized one on
//     the next poll (kills the poster swap).
//
//  2. season_media_texts (base lang only) — one poster row per season whose
//     tv.Seasons[i].PosterPath is non-empty. GetTVAllLangs pins the en-US root,
//     so the root season poster IS the en-US image. Non-base season langs come
//     from RefreshSeasonSlim (strict), not here.
//
//  3. Canon preservation Upsert — copies the 6 bare-excluded canon columns
//     (tvdb_id/imdb_id/next_air_date/year/runtime_minutes/in_production) from
//     canon.Get so the narrow write does NOT blank previously-merged Sonarr
//     values, plus per-season air_date/tmdb_season_id freshness. Canon no
//     longer carries poster/backdrop columns (Variant A / S-E3b) — all art
//     lives in the *_media_texts side-tables above.
//
//  4. series.enrichment_media_synced_at = now (MarkMediaSynced) — the stamp is
//     now backed by REAL per-lang art writes, not a no-op.
//
// Each written media-text row eager-resolves its hashes via MediaResolver
// (poster w342/poster_w342, backdrop w1280/backdrop_w1280, season poster
// w342/poster_w342) — the returned hash is stored on the row and, as a side
// effect, EnsurePending pre-warms the media pipeline. The hashes are resolved
// OUTSIDE the tx (mirrors RefreshSeriesAllLangs): Resolve does its own
// media_assets write via a different Gorm session; nesting inside A4's tx would
// risk cross-table lock contention. Idempotent (content-addressed ON CONFLICT
// DO NOTHING), so a later tx failure costs only a cheap re-resolve next call.
//
// HARD RULE — ONE TMDB CALL: exactly 1 TMDB call (GetTVAllLangs). The lang
// param is retained ONLY for the probe VO + log correlation; the fetch is no
// longer lang-specific.
//
// force semantics (PLAN §6.-1 F-R2-5):
//   - force=true  → bypass Probe TTL gate. Always fetch + write. Used by
//     Phase 4 ChangesSyncer when TMDB's /tv/{id}/changes signals
//     {key=images} — trust TMDB over local TTL.
//   - force=false → respect Probe verdict if Probe was injected. Section
//     SectionMedia Stale=false → early return nil. Probe nil →
//     fetch unconditionally (caller A5 EnsureFreshScope already gated).
//
// Tx shape: TMDB GetTVAllLangs (out of tx) → build per-lang media-text rows +
// eager Resolve (out of tx) → tx{ Series.Upsert (6 preservation copies) →
// for-each-season Seasons.Upsert (air_date/tmdb_season_id) → N×series_media_texts
// Upsert → M×season_media_texts Upsert → Series.MarkMediaSynced } → commit.
//
// Ports nil-OK: SeriesMediaTexts / SeasonMediaTexts / MediaResolver nil → skip
// that write (constructor treats them nil-OK). canon.TMDBID nil → no-op
// (Sonarr-only series cannot be TMDB-enriched). Mirrors handleInternal's
// no_tmdb_id_skip branch + A2/A3a/A3b patterns.
//
// No enrichment_errors journaling — narrow methods caller-orchestrated
// (A5/ChangesSyncer handle retry); Handle/HandleForced dispatcher-driven
// path retains the error-row write.
//
// UNIVERSAL NARROW-WRITER AUDIT (per A3a lesson) — the Series.Upsert
// contains 6 columns that are bare `excluded.X` in seriesUpsertAssignments
// (tvdb_id/imdb_id/next_air_date/year/runtime_minutes/in_production —
// Sonarr's authoritative fields per PRD §5.4 that MUST NOT be COALESCE-
// wrapped or Sonarr year overrides break). A4's canonPayload copies those
// 6 fields from `canon.Get` result inline so the narrow write does NOT
// nuke previously-merged values. See seriesUpsertAssignments audit table
// in story 562 for the field-by-field verdict.
func (w *SeriesWorker) RefreshMediaAssets(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
	force bool,
) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("op", "refresh_media_assets"),
		slog.Int64("entity_id", int64(seriesID)),
		slog.String("language", lang),
		slog.Bool("force", force),
	)

	// 1. Validate lang via VO (defensive; canonical images are lang-
	//    agnostic but the parameter is preserved for TMDB call symmetry
	//    with A2/A3a/A3b and log correlation).
	langVO, err := values.NewLanguageTag(lang)
	if err != nil {
		return fmt.Errorf("refresh_media_assets: invalid lang %q: %w", lang, err)
	}

	// 2. Read canon to get tmdb_id + the 6 preservation-copy fields.
	//    Missing canon row → log + return nil (mirrors A2/A3a/A3b
	//    series_missing branch).
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.refresh_media_assets.series_missing",
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("refresh_media_assets: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.series.refresh_media_assets.no_tmdb_id_skip")
		return nil
	}

	// 3. TTL gate (force=false + Probe non-nil only). SectionMedia is
	//    lang-agnostic per probe.go:182-186 — pass nil seasonNumbers
	//    (media section is series-level, not sparse-per-season). Fail-open
	//    per A1 Radarr lesson.
	if !force && w.deps.Probe != nil {
		verdicts, perr := w.deps.Probe.IsStale(ctx, seriesID, langVO, nil)
		if perr != nil {
			log.WarnContext(ctx, "enrichment.series.refresh_media_assets.probe_error",
				slog.String("error", perr.Error()))
		} else {
			for _, v := range verdicts {
				if v.Section == freshener.SectionMedia && !v.Stale {
					log.DebugContext(ctx, "enrichment.series.refresh_media_assets.skip_fresh",
						slog.String("reason", v.Reason))
					return nil
				}
			}
		}
	}

	// 4. TMDB call — ONE call, GetTVAllLangs (union include_image_language,
	//    lang-agnostic — matches SectionMedia's lang-agnostic probe). The lang
	//    param above is retained ONLY for the probe VO + log correlation; the
	//    fetch is no longer lang-specific. Per-lang art is picked from
	//    tv.Images below (base: full priority + root fallback; non-base: strict
	//    exact-lang, skip-if-absent to avoid poisoning).
	tmdbID := int64(*canon.TMDBID)
	tv, err := w.deps.TMDB.GetTVAllLangs(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("refresh_media_assets: GetTVAllLangs(%d): %w", tmdbID, err)
	}
	if tv == nil {
		log.WarnContext(ctx, "enrichment.series.refresh_media_assets.empty_response")
		return nil
	}

	// 5. Build minimal canon payload — series-level media fields only
	//    PLUS the 6 preservation copies from canon.Get. Rationale: the 6
	//    columns (tvdb_id/imdb_id/next_air_date/year/runtime_minutes/
	//    in_production) use bare `excluded.X` in seriesUpsertAssignments
	//    per PRD §5.4 (Sonarr year authority must survive a Sonarr-driven
	//    scan). A narrow writer that leaves those fields nil in the
	//    payload would blank the previously-merged values on every A4
	//    fire → Story 552 regression class.
	//
	//    Defensive on media columns: even though seriesUpsertAssignments
	//    COALESCE'd poster/backdrop, we ONLY set them when TMDB returned
	//    non-empty (matches the 346 defensive write-side guard shape). If
	//    TMDB returns empty poster_path (rare — indicates true poster
	//    removal upstream), we leave nil and let COALESCE preserve the
	//    prior value.
	canonPayload := series.Canon{
		ID:        seriesID,
		TMDBID:    canon.TMDBID,
		Hydration: canon.Hydration, // preserve existing (full/stub) — CASE-expr assignment keeps 'full' sticky
		// S-E3a — canon no longer carries Title / PosterAsset / BackdropAsset.
		// Series art is written per-language into series_media_texts by the
		// text-refresh path; this narrow writer only preserves non-localizable
		// fields + stamps enrichment_media_synced_at below.
		// Preservation copies — bare `excluded.X` in seriesUpsertAssignments
		// would overwrite these to nil / zero-value with A4's narrow shape.
		// Copied from canon.Get result to preserve prior TMDB-enriched values.
		// Sonarr's authoritative fields (Year — per PRD §5.4) still get
		// overridden by the next Sonarr scan; A4 preserves the current merged
		// state so a narrow media refresh does not silently drop them.
		TVDBID:         canon.TVDBID,
		IMDBID:         canon.IMDBID,
		NextAirDate:    canon.NextAirDate,
		Year:           canon.Year,
		RuntimeMinutes: canon.RuntimeMinutes,
		InProduction:   canon.InProduction,
	}

	// 6. Build per-season payloads. Skip seasons TMDB doesn't ship a
	//    poster for (empty PosterPath means TMDB has no image) — the
	//    filter drops those rows entirely rather than passing nil
	//    PosterAsset, so bare `excluded.poster_asset` never sees a nil
	//    write (Story 552 class avoided). tv.Seasons[] shape matches
	//    MapTVToSeasons at mappers.go:98-115.
	//
	//    Per the universal narrow-writer audit: also populate Name /
	//    Overview / AirDate / TMDBSeasonID from tv.Seasons[i]. Those
	//    columns are bare `excluded.X` in seasonsUpsertAssignments (name/
	//    overview/air_date are TMDB-owned per convention; tmdb_season_id
	//    is bare-excluded for FK stability). Leaving them nil in the
	//    payload would blank previously-populated values on every A4 fire
	//    (Story 552 mirror class). EpisodeCount + EpisodesSyncedAt stay
	//    nil — both are COALESCE-wrapped post-A3a and untouched by A4.
	type seasonMediaPayload struct {
		season series.CanonSeason
		raw    *string
	}
	seasonPayloads := make([]seasonMediaPayload, 0, len(tv.Seasons))
	for _, s := range tv.Seasons {
		if s.PosterPath == "" {
			continue
		}
		p := s.PosterPath
		// S-E3a — canon season no longer carries Name / Overview /
		// PosterAsset. Per-season art flows into season_media_texts (S-C2);
		// this narrow writer keeps only air_date / tmdb_season_id fresh and
		// warms the media cache via the eager-hash side effect below.
		cs := series.CanonSeason{
			SeriesID:     seriesID,
			SeasonNumber: s.SeasonNumber,
			AirDate:      parseDateOrNilSlim(s.AirDate),
		}
		if s.ID != 0 {
			id := int(s.ID)
			cs.TMDBSeasonID = &id
		}
		seasonPayloads = append(seasonPayloads, seasonMediaPayload{
			season: cs,
			raw:    &p,
		})
	}

	now := w.deps.Clock()

	// 7. Build per-language series_media_texts rows OUTSIDE the tx. Eager
	//    MediaResolver.Resolve mints the poster/backdrop hashes (side effect:
	//    EnsurePending pre-warms the media pipeline); nesting that inside the
	//    tx risks cross-table lock contention (RefreshSeriesAllLangs pattern).
	//    BASE lang: full priority pick + root fallback → guaranteed en-US row.
	//    NON-base: strict exact-lang pick, no root fallback (never poison a
	//    language row with mismatched art). Story 1081a: a row is ALWAYS
	//    persisted for every supported language, even when no art was found —
	//    poster_checked_at/backdrop_checked_at are stamped so the reader can
	//    tell "confirmed absent" apart from "never checked".
	seriesMediaWrites := make([]series.SeriesMediaText, 0, len(locale.SupportedUserLanguages))
	if w.deps.SeriesMediaTexts != nil {
		base := locale.Default()
		for _, l := range locale.SupportedUserLanguages {
			var posterPath, backdropPath *string
			if l == base {
				posterPath = pickPosterForLangRooted(tv.Images, l, tv.PosterPath)
				backdropPath = pickBackdropForLang(tv.Images, l)
				if backdropPath == nil {
					backdropPath = nonEmptyStringPtr(tv.BackdropPath)
				}
			} else {
				posterPath = pickPosterForLangStrict(tv.Images, l)
				// W18-15 — non-base backdrop parity with RefreshSeriesAllLangs
				// (series_worker_refresh_all_langs.go:161-168). Backdrops are
				// textless key-art (no baked-in localized title), so the
				// neutral → lang → en → root ladder cannot poison a language row
				// the way a poster would. The old strict pick left a poster-only
				// ru row (backdrop NULL) whenever TMDB carried a ru poster but
				// only neutral/en backdrops, forcing the hero to render a
				// placeholder. POSTER stays STRICT (#977/#978 — per-lang poster
				// art is intentional).
				backdropPath = pickBackdropForLang(tv.Images, l)
				if backdropPath == nil {
					backdropPath = nonEmptyStringPtr(tv.BackdropPath)
				}
			}
			// Story 1081a — DO NOT skip an art-less lang. Persist an absence
			// row (asset NULL + *_checked_at = now) so the reader can tell
			// "confirmed-absent" from "never checked". The COALESCE-guarded
			// asset columns keep any previously-fetched art; only the plain-
			// excluded *_checked_at markers advance.
			var posterHash, backdropHash *string
			if w.deps.MediaResolver != nil {
				if posterPath != nil {
					posterHash = w.deps.MediaResolver.Resolve(ctx, posterPath, "w342", "poster_w342")
				}
				if backdropPath != nil {
					backdropHash = w.deps.MediaResolver.Resolve(ctx, backdropPath, "w1280", "backdrop_w1280")
				}
			}
			seriesMediaWrites = append(seriesMediaWrites, series.SeriesMediaText{
				SeriesID:          seriesID,
				Language:          l,
				PosterAsset:       posterPath,
				PosterHash:        posterHash,
				BackdropAsset:     backdropPath,
				BackdropHash:      backdropHash,
				EnrichedAt:        &now,
				PosterCheckedAt:   &now,
				BackdropCheckedAt: &now,
			})
		}
	}

	// 8. Build base-lang season_media_texts rows OUTSIDE the tx. GetTVAllLangs
	//    pins en-US root, so each non-empty tv.Seasons[i].PosterPath IS the
	//    en-US season poster. Non-base season langs come from RefreshSeasonSlim.
	seasonMediaWrites := make([]series.SeasonMediaText, 0, len(seasonPayloads))
	if w.deps.SeasonMediaTexts != nil {
		for _, sp := range seasonPayloads {
			var posterHash *string
			if w.deps.MediaResolver != nil {
				posterHash = w.deps.MediaResolver.Resolve(ctx, sp.raw, "w342", "poster_w342")
			}
			seasonMediaWrites = append(seasonMediaWrites, series.SeasonMediaText{
				SeriesID:     seriesID,
				SeasonNumber: sp.season.SeasonNumber,
				Language:     locale.Default(),
				PosterAsset:  sp.raw,
				PosterHash:   posterHash,
				EnrichedAt:   &now,
			})
		}
	}

	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		// 9a. Series canon preservation upsert — narrow shape; the explicit
		//     preservation copies protect the 6 bare-excluded columns.
		if _, err := w.deps.Series.Upsert(txCtx, canonPayload); err != nil {
			return fmt.Errorf("upsert series canon (media narrow): %w", err)
		}

		// 9b. Per-season canon upserts — air_date/tmdb_season_id freshness
		//     (skip-if-empty-poster filter applied above).
		for _, sp := range seasonPayloads {
			if _, err := w.deps.Seasons.Upsert(txCtx, sp.season); err != nil {
				return fmt.Errorf("upsert season %d (media narrow): %w",
					sp.season.SeasonNumber, err)
			}
		}

		// 9c. Per-language series art — the actual localized poster/backdrop.
		for _, row := range seriesMediaWrites {
			if err := w.deps.SeriesMediaTexts.Upsert(txCtx, row); err != nil {
				return fmt.Errorf("upsert series_media_texts (lang=%s): %w", row.Language, err)
			}
		}

		// 9d. Base-lang season art.
		for _, row := range seasonMediaWrites {
			if err := w.deps.SeasonMediaTexts.Upsert(txCtx, row); err != nil {
				return fmt.Errorf("upsert season_media_texts (season=%d): %w", row.SeasonNumber, err)
			}
		}

		// 9e. Stamp series.enrichment_media_synced_at — now backed by real
		//     art writes. Defensive against subsequent Sonarr Upsert thanks to
		//     seriesUpsertAssignments COALESCE (already shipped A2).
		if err := w.deps.Series.MarkMediaSynced(txCtx, seriesID, now); err != nil {
			return fmt.Errorf("mark series media synced: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh_media_assets: tx: %w", err)
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	log.InfoContext(ctx, "enrichment.series.refresh_media_assets.ok",
		slog.Bool("poster_present", tv.PosterPath != ""),
		slog.Bool("backdrop_present", tv.BackdropPath != ""),
		slog.Int("series_media_rows", len(seriesMediaWrites)),
		slog.Int("season_media_rows", len(seasonMediaWrites)),
		slog.Int("seasons_with_posters", len(seasonPayloads)),
		slog.Int("duration_ms", durMs),
	)
	return nil
}
