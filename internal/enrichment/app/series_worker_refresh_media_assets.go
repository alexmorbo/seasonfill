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
)

// RefreshMediaAssets fetches /tv/{tmdb_id}?language={lang} (with
// append_to_response=…,images already bundled in tvAppendToResponse) and
// writes:
//
//  1. series canon paths — series.poster_asset (from tv.PosterPath) +
//     series.backdrop_asset (from tv.BackdropPath) via Series.Upsert.
//     Existing COALESCE on both columns (seriesUpsertAssignments line
//     792-793) preserves prior TMDB-enriched values when a concurrent
//     Sonarr scan writes nil. A4's writer ALWAYS populates when TMDB
//     returns non-empty — matches the defensive write-side guard shape
//     shipped in Story 346.
//
//  2. per-season paths — seasons.poster_asset (from tv.Seasons[i].PosterPath)
//     via Seasons.Upsert per season. Bare `excluded.poster_asset` in
//     seasonsUpsertAssignments is intentional (TMDB-owned refresh-on-write
//     convention, seasons_repository.go:158); A4 writer always populates
//     when TMDB returns non-empty — Story 552 class NOT reintroduced
//     because the writer never leaves the field unset AND the skip-if-empty
//     filter drops nil-path rows outright.
//
//  3. MediaResolver.Resolve inline call per non-empty path — mints eager
//     sha256 hash + writes media_assets pending row via EnsurePending
//     (Story 347 unified-resolve contract). Composer's next cold
//     /series/{id}?lang=X read has a stable hash handle immediately —
//     closes operator smoke symptom #2 (grey placeholder → poster hash →
//     media pipeline fills bytes).
//
//  4. series.enrichment_media_synced_at = now (MarkMediaSynced stamp).
//
// Steps 1, 2, 4 inside ONE tx. Step 3 (MediaResolver.Resolve) called
// OUTSIDE the tx — Resolve internally does its own DB write (EnsurePending
// on media_assets via a different Gorm session); nesting inside A4's tx
// would risk cross-table lock contention (media_assets has its own natural
// key + ON CONFLICT DO NOTHING). Design rationale: the eager-hash call is
// IDEMPOTENT (existing pending/stored row preserved by DO NOTHING), so if
// step 3 fires but a later hypothetical step fails, next A4 call re-resolves
// cheaply. Loss surface: NONE (media_assets is content-addressed —
// re-EnsurePending same hash+URL is a no-op).
//
// HARD RULE — ONE TMDB CALL: exactly 1 TMDB call (`GetTV(?language=lang)`);
// lang is validated + logged for correlation. A4 writes the CANON media
// columns (series.poster_asset/backdrop_asset, seasons.poster_asset) using
// TMDB's canonical best-picks (tv.PosterPath / tv.BackdropPath /
// tv.Seasons[i].PosterPath). Per-language poster ranking from
// tv.Images.Posters[] IS implemented, but in the sibling per-lang writers
// (series_media_texts 584a, season_media_texts S-C2), not here.
// TODO(S-E3): these canon media writes are slated for removal once the
// localizable canon columns are dropped — art will live only in the
// *_media_texts side-tables.
//
// force semantics (PLAN §6.-1 F-R2-5):
//   - force=true  → bypass Probe TTL gate. Always fetch + write. Used by
//     Phase 4 ChangesSyncer when TMDB's /tv/{id}/changes signals
//     {key=images} — trust TMDB over local TTL.
//   - force=false → respect Probe verdict if Probe was injected. Section
//     SectionMedia Stale=false → early return nil. Probe nil →
//     fetch unconditionally (caller A5 EnsureFreshScope already gated).
//
// Tx shape: TMDB GetTV (out of tx) → tx{ Series.Upsert (canon paths +
// 6 preservation copies from canon.Get) → for-each-season{ Seasons.Upsert
// (canonSeason with poster_asset + Name/Overview/AirDate/TMDBSeasonID
// populated from tv.Seasons[i]) } → Series.MarkMediaSynced } → commit →
// (post-commit) MediaResolver.Resolve per non-empty path (best-effort,
// returned hash pointer discarded; the side-effect is the media_assets
// pending row).
//
// canon.TMDBID nil → no-op (Sonarr-only series cannot be TMDB-enriched).
// Mirrors handleInternal's no_tmdb_id_skip branch + A2/A3a/A3b patterns.
//
// No enrichment_errors journaling — narrow methods caller-orchestrated
// (A5/ChangesSyncer handle retry); Handle/HandleForced dispatcher-driven
// path retains the error-row write.
//
// UNIVERSAL NARROW-WRITER AUDIT (per A3a lesson) — Path 1 Series.Upsert
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
		slog.String("domain", "enrichment"),
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

	// 4. TMDB call — ONE call, reuses existing GetTV (append_to_response
	//    includes 'images' via tvAppendToResponse const — see tmdb/tv.go:16).
	//    A4 writes canon columns from tv.PosterPath / tv.BackdropPath /
	//    tv.Seasons[i].PosterPath (TMDB's canonical picks). Per-lang art
	//    from tv.Images.Posters[] is handled by the *_media_texts writers.
	//    TODO(S-E3): drop these canon media writes with the canon columns.
	tmdbID := int64(*canon.TMDBID)
	tv, err := w.deps.TMDB.GetTV(ctx, tmdbID, lang)
	if err != nil {
		return fmt.Errorf("refresh_media_assets: GetTV(%d, lang=%s): %w", tmdbID, lang, err)
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
		ID:            seriesID,
		TMDBID:        canon.TMDBID,
		Hydration:     canon.Hydration, // preserve existing (full/stub) — CASE-expr assignment keeps 'full' sticky
		Title:         canon.Title,     // required by Upsert non-null; COALESCE on other fields preserves
		PosterAsset:   canonPosterOrRoot(tv),
		BackdropAsset: canonBackdropOrRoot(tv),
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
		cs := series.CanonSeason{
			SeriesID:     seriesID,
			SeasonNumber: s.SeasonNumber,
			PosterAsset:  &p,
			Name:         nonEmptyStringPtr(s.Name),
			Overview:     nonEmptyStringPtr(s.Overview),
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
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		// 7a. Series canon upsert — narrow shape, COALESCE preserves prior
		//     media columns; explicit preservation copies protect the 6
		//     bare-excluded columns.
		if _, err := w.deps.Series.Upsert(txCtx, canonPayload); err != nil {
			return fmt.Errorf("upsert series canon (media narrow): %w", err)
		}

		// 7b. Per-season upserts — poster_asset always populated (skip-if-
		//     empty filter above guarantees this); Name/Overview/AirDate/
		//     TMDBSeasonID also populated to avoid Story 552 blanking
		//     regression on bare-excluded seasons.{name, overview,
		//     air_date, tmdb_season_id}.
		for _, sp := range seasonPayloads {
			if _, err := w.deps.Seasons.Upsert(txCtx, sp.season); err != nil {
				return fmt.Errorf("upsert season %d (media narrow): %w",
					sp.season.SeasonNumber, err)
			}
		}

		// 7c. Stamp series.enrichment_media_synced_at — defensive against
		//     subsequent Sonarr Upsert thanks to seriesUpsertAssignments
		//     COALESCE (line 818, already shipped A2).
		if err := w.deps.Series.MarkMediaSynced(txCtx, seriesID, now); err != nil {
			return fmt.Errorf("mark series media synced: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh_media_assets: tx: %w", err)
	}

	// 8. MediaResolver eager-hash side effect (POST-TX, best-effort).
	//    Each Resolve call triggers:
	//      - HashForSourceURL lookup — miss on cold
	//      - Under Story 347 unified-resolve: EnsurePending(eagerHash, URL, kind)
	//        writes a media_assets pending row (idempotent — ON CONFLICT DO NOTHING)
	//      - Returns *string sha256-hex hash pointer
	//    A4 discards the returned hash pointer — the SIDE EFFECT (pending
	//    row written) is what closes operator symptom #2.
	//
	//    nil MediaResolver → skip (nil-OK per SeriesWorkerDeps.MediaResolver
	//    doc). Called OUTSIDE the tx above deliberately — media_assets is a
	//    different natural key (hash primary) with its own ON CONFLICT
	//    DO NOTHING; nesting inside A4's tx would risk cross-table lock
	//    contention. Idempotent — subsequent A4 calls re-mint hashes
	//    without side effect (existing pending/stored rows preserved).
	if w.deps.MediaResolver != nil {
		// Series poster: both grid + hero variants mirror
		// composePrewarmAssets kinds (series_worker_prewarm.go).
		if tv.PosterPath != "" {
			p := tv.PosterPath
			_ = w.deps.MediaResolver.Resolve(ctx, &p, "w342", "poster_w342")
			_ = w.deps.MediaResolver.Resolve(ctx, &p, "w780", "poster_w780")
		}
		if tv.BackdropPath != "" {
			b := tv.BackdropPath
			_ = w.deps.MediaResolver.Resolve(ctx, &b, "w1280", "backdrop_w1280")
		}
		for _, sp := range seasonPayloads {
			// Season poster: w154 matches composePrewarmAssets kind
			// + composer.go read shape Resolve(rawPath, "w154",
			// "season_poster_w154").
			if sp.raw != nil && *sp.raw != "" {
				_ = w.deps.MediaResolver.Resolve(ctx, sp.raw, "w154", "season_poster_w154")
			}
		}
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	log.InfoContext(ctx, "enrichment.series.refresh_media_assets.ok",
		slog.Bool("poster_present", tv.PosterPath != ""),
		slog.Bool("backdrop_present", tv.BackdropPath != ""),
		slog.Int("seasons_with_posters", len(seasonPayloads)),
		slog.Int("duration_ms", durMs),
	)
	return nil
}

// canonPosterOrRoot returns the language-agnostic canonical poster from
// tv.Images (nil → en) with a fall-through to the root tv.PosterPath. S-A:
// keeps the series canon poster neutral/English rather than whatever the
// call-language root happened to be.
func canonPosterOrRoot(tv *tmdb.TVResponse) *string {
	if tv == nil {
		return nil
	}
	if p := pickCanonicalPoster(tv.Images); p != nil {
		return p
	}
	return nonEmptyStringPtr(tv.PosterPath)
}

// canonBackdropOrRoot mirrors canonPosterOrRoot for the backdrop.
func canonBackdropOrRoot(tv *tmdb.TVResponse) *string {
	if tv == nil {
		return nil
	}
	if b := pickCanonicalBackdrop(tv.Images); b != nil {
		return b
	}
	return nonEmptyStringPtr(tv.BackdropPath)
}
