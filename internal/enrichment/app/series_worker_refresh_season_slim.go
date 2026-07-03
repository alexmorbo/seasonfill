package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// RefreshSeasonSlim fetches /tv/{tmdb_id}/season/{seasonNumber}?language={lang}
// and writes episodes + episode_texts.{episode_id, lang} for ONE season +
// ONE lang atomically in a single tx, then stamps seasons.episodes_synced_at
// for that (series_id, season_number).
//
// HARD RULE — ONE LANG ONLY (PLAN §4.1): exactly 1 TMDB call, episode_texts
// rows only for the requested lang.
//
// HARD RULE — ONE SEASON ONLY: caller specifies seasonNumber explicitly;
// other seasons untouched.
//
// force semantics (PLAN §6.-1 F-R2-5):
//   - force=true  → bypass Probe TTL gate. Always fetch + write. Used
//     by Phase 4 ChangesSyncer when TMDB's /tv/{id}/changes signals
//     {key=episodes season_number=N} — trust TMDB over the local TTL.
//   - force=false → respect Probe verdict if Probe was injected into
//     deps. SeasonSection(seasonNumber) Stale=false → early return nil.
//     Probe nil → fetch unconditionally (caller A5 EnsureFreshScope
//     already gated).
//
// Tx shape: TMDB GetSeason (out of tx) → Seasons.Upsert (in tx — defensive
// stub upsert; preserves season_id for episodes FK) → SeasonTexts.Upsert
// (in tx — B3b; localised season name/overview from the SAME GetSeason
// payload; nil-OK dep + skipped when both fields empty; COALESCE-preserve
// lives in the repo) → Episodes.BatchUpsert (in tx) → EpisodeTexts.Upsert
// per episode (in tx) → MarkSeasonEpisodesSynced UPDATE (in tx) → commit.
// tx rollback if any write fails; stamp is NEVER written without a
// successful episodes+texts UPSERT.
//
// canon.TMDBID nil → no-op (Sonarr-only series cannot be TMDB-enriched).
//
// No enrichment_errors journaling — narrow methods caller-orchestrated
// (A5/ChangesSyncer handle retry); Handle/HandleForced dispatcher-driven
// path retains the error-row write.
//
// Per-episode UPSERT (NOT TRUNCATE+INSERT) — preserves episode_states FK
// references through ON CONFLICT DO UPDATE (existing episodes_repository
// natural-key behavior).
func (w *SeriesWorker) RefreshSeasonSlim(
	ctx context.Context,
	seriesID domain.SeriesID,
	seasonNumber int,
	lang string,
	force bool,
) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("domain", "enrichment"),
		slog.String("op", "refresh_season_slim"),
		slog.Int64("entity_id", int64(seriesID)),
		slog.Int("season_number", seasonNumber),
		slog.String("language", lang),
		slog.Bool("force", force),
	)

	// 1. Validate lang via VO (defensive; raw string preserved for
	//    TMDB call + episode_texts.language column).
	langVO, err := values.NewLanguageTag(lang)
	if err != nil {
		return fmt.Errorf("refresh_season_slim: invalid lang %q: %w", lang, err)
	}

	// 2. Read canon to get tmdb_id. Missing canon row → log + return nil
	//    (same shape as A2 series_missing branch).
	canon, err := w.deps.Series.Get(ctx, seriesID)
	if err != nil {
		var seriesNF *sharedErrors.SeriesNotFoundError
		if errors.As(err, &seriesNF) {
			log.WarnContext(ctx, "enrichment.series.refresh_season_slim.series_missing",
				slog.String("code", seriesNF.Code()))
			return nil
		}
		return fmt.Errorf("refresh_season_slim: load canon: %w", err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.series.refresh_season_slim.no_tmdb_id_skip")
		return nil
	}

	// 3. TTL gate (force=false + Probe non-nil only). SPARSE 1 season
	//    verdict requested. Fail-open per A1 Radarr lesson.
	if !force && w.deps.Probe != nil {
		verdicts, perr := w.deps.Probe.IsStale(ctx, seriesID, langVO, []int{seasonNumber})
		if perr != nil {
			log.WarnContext(ctx, "enrichment.series.refresh_season_slim.probe_error",
				slog.String("error", perr.Error()))
		} else {
			for _, v := range verdicts {
				if n, ok := freshener.IsSeasonSection(v.Section); ok && n == seasonNumber && !v.Stale {
					log.DebugContext(ctx, "enrichment.series.refresh_season_slim.skip_fresh",
						slog.String("reason", v.Reason))
					return nil
				}
			}
		}
	}

	// 4. TMDB call — ONE LANG ONLY, ONE SEASON ONLY.
	tmdbID := int64(*canon.TMDBID)
	seasonResp, err := w.deps.TMDB.GetSeason(ctx, tmdbID, seasonNumber, lang)
	if err != nil {
		return fmt.Errorf("refresh_season_slim: GetSeason(%d, %d, lang=%s): %w",
			tmdbID, seasonNumber, lang, err)
	}
	if seasonResp == nil {
		log.WarnContext(ctx, "enrichment.series.refresh_season_slim.empty_response")
		return nil
	}

	// 5. Build canonical season payload (defensive UPSERT — A5 caller
	//    may invoke without prior Handle ensuring the row exists).
	//    EpisodeCount derived from the authoritative response payload
	//    (len(Episodes)) — tmdb.SeasonResponse omits a series-level
	//    EpisodeCount field (only tmdb.SeasonShort in TVResponse.Seasons
	//    carries it), so we populate it ourselves. Defense-in-depth
	//    paired with seasonsUpsertAssignments COALESCE on episode_count
	//    (see seasons_repository.go) — if a future narrow writer leaves
	//    the field unset the COALESCE wrap still preserves the prior
	//    value (Story 552 regression class).
	// S-E3a — Name / Overview / PosterAsset dropped from canon season;
	// per-language season name/overview go to season_texts and the poster
	// to season_media_texts (both written below).
	seasonPayload := series.CanonSeason{
		SeriesID:     seriesID,
		SeasonNumber: seasonNumber,
		TMDBSeasonID: nonZeroIntPtrSlim(int(seasonResp.ID)),
		AirDate:      parseDateOrNilSlim(seasonResp.AirDate),
		EpisodeCount: nonZeroIntPtrSlim(len(seasonResp.Episodes)),
	}

	now := w.deps.Clock()

	// Prepare season_texts writes for ALL supported languages from the SAME
	// GetSeason(+translations) payload (S-C). Built OUTSIDE the tx (pure
	// projection, no I/O); the tx just replays the slice. nil-OK dep: when
	// SeasonTexts is not wired the slice is empty and the tx skips the step.
	var seasonTextWrites []series.SeasonText
	if w.deps.SeasonTexts != nil {
		seasonTextWrites = buildSeasonTextWrites(seasonResp, seriesID, seasonNumber, lang, now)
	}

	// S-C2: per-language SEASON poster writes from the SAME GetSeason(+images)
	// payload. Built OUTSIDE the tx — buildSeasonMediaTextWrites eager-resolves
	// each poster hash (MediaResolver.Resolve → media_assets pending row +
	// download enqueue); nesting those cross-table writes inside the season_texts
	// tx risks lock contention (refresh_text.go pattern). nil-OK dep.
	var seasonMediaWrites []series.SeasonMediaText
	if w.deps.SeasonMediaTexts != nil {
		seasonMediaWrites = buildSeasonMediaTextWrites(
			ctx, w.deps.MediaResolver, seasonResp, seriesID, seasonNumber, lang, now)
	}

	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		// 5a. Upsert season row (lightweight — gets season_id for
		//     episode FK). Existing-row update (if a prior full-canon
		//     hydration touched it) preserves enrichment-side fields
		//     via seasonsUpsertAssignments COALESCE; the new payload
		//     only overrides Name/Overview/AirDate/PosterAsset which
		//     are TMDB-owned.
		seasonID, err := w.deps.Seasons.Upsert(txCtx, seasonPayload)
		if err != nil {
			return fmt.Errorf("upsert season: %w", err)
		}

		// 5a-bis. season_texts UPSERT for ALL supported languages (S-C,
		//   generalises Story 581's single-lang write). Rows come from the
		//   SAME seasonResp(+translations) already fetched above — NO second
		//   TMDB call. Keyed on (series_id, season_number, language),
		//   independent of season_id, so it runs right after the season mint
		//   and is NOT gated by len(episodes)==0.
		//
		//   Content is prepared by buildSeasonTextWrites (see its O-4 note on
		//   why episodes[] do NOT get the all-langs benefit). Empty projections
		//   are already skipped there; the repo COALESCE additionally preserves
		//   prior values on partial writes.
		for _, st := range seasonTextWrites {
			if err := w.deps.SeasonTexts.Upsert(txCtx, st); err != nil {
				return fmt.Errorf("upsert season_texts (season=%d, lang=%s): %w",
					seasonNumber, st.Language, err)
			}
		}

		// 5a-ter. season_media_texts UPSERT for the langs computed above (S-C2).
		//   Same seasonResp(+images) — NO second TMDB call. Keyed on
		//   (series_id, season_number, language), independent of season_id;
		//   COALESCE in the repo preserves prior values on partial writes.
		for _, sm := range seasonMediaWrites {
			if err := w.deps.SeasonMediaTexts.Upsert(txCtx, sm); err != nil {
				return fmt.Errorf("upsert season_media_texts (season=%d, lang=%s): %w",
					seasonNumber, sm.Language, err)
			}
		}

		// 5b. Build canonical episodes (lang-agnostic shape +
		//     TMDB-owned still/runtime/rating).
		episodes := tmdb.MapSeasonToEpisodes(seasonResp, seriesID, seasonID)
		if len(episodes) == 0 {
			// TMDB returned a season with empty Episodes[] — happens
			// for future-scheduled seasons. Still stamp synced_at: probe
			// won't re-trigger for TTL window.
			log.WarnContext(txCtx, "enrichment.series.refresh_season_slim.empty_episodes")
		}

		// 5c. Batch upsert episodes — preserves episode.id on natural-key
		//     conflict (series_id, season_number, episode_number).
		episodeIDs, err := w.deps.Episodes.BatchUpsert(txCtx, episodes)
		if err != nil {
			return fmt.Errorf("batch upsert episodes: %w", err)
		}

		// 5d. Per-episode_texts.{episode_id, lang} UPSERT — pull
		//     localised Name/Overview from the same TMDB response (TMDB
		//     §4.2 fallback: if no requested-lang translation, returns
		//     original; we trust blindly and persist whatever came back).
		for i, ep := range seasonResp.Episodes {
			if i >= len(episodeIDs) {
				break
			}
			text := series.EpisodeText{
				EpisodeID: domain.EpisodeID(episodeIDs[i]),
				Language:  lang,
				Title:     nonEmptyStringPtr(ep.Name),
				Overview:  nonEmptyStringPtr(ep.Overview),
			}
			if err := w.deps.EpisodeTexts.Upsert(txCtx, text); err != nil {
				return fmt.Errorf("upsert episode_texts (episode=%d): %w", ep.EpisodeNumber, err)
			}
		}

		// 5e. Stamp seasons.episodes_synced_at — defensive against
		//     subsequent Sonarr Upsert thanks to seasonsUpsertAssignments
		//     COALESCE protection.
		if err := w.deps.Seasons.MarkSeasonEpisodesSynced(txCtx, seriesID, seasonNumber, now); err != nil {
			return fmt.Errorf("mark season episodes synced: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("refresh_season_slim: tx: %w", err)
	}

	durMs := int(w.deps.Clock().Sub(start).Milliseconds())
	log.InfoContext(ctx, "enrichment.series.refresh_season_slim.ok",
		slog.Int("episodes_upserted", len(seasonResp.Episodes)),
		slog.Int("duration_ms", durMs))
	return nil
}

// nonZeroIntPtrSlim returns &v if v != 0, else nil. Scoped locally
// so series_worker_refresh_season_slim has no dependency on tmdb-internal
// helpers (mirror of tmdb/mappers.go nonZeroIntPtr).
func nonZeroIntPtrSlim(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// parseDateOrNilSlim — local helper for AirDate string. Empty / unparsable
// strings → nil. Matches parseDate behavior in tmdb/mappers.go without
// exposing that helper through the package boundary.
func parseDateOrNilSlim(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

// buildSeasonTextWrites projects a GetSeason(+translations) payload into one
// season_texts row per supported user language (S-C all-langs). Pure function;
// no I/O — the caller replays the slice inside the write tx.
//
// O-4 (fundamental TMDB limitation, NOT a defect): GetSeason(+translations)
// returns all languages ONLY for the SEASON name/overview. The episodes[]
// array in the SAME response is single-lang (the call language). TMDB has NO
// bulk episode-translation endpoint, so episode_texts do NOT get the all-langs
// benefit — they are written call-lang now (step 5d) and the second language
// is caught up later by a per-lang GetSeason via the existing TTL mechanism.
// Do NOT attempt to all-langs the episodes here.
//
// Row policy (mirrors RefreshSeriesAllLangs / S-B D2):
//   - The CALL language is ALWAYS written: the ROOT Name/Overview are that
//     language's source (GetSeason localises the root to the call lang), so a
//     per-field root fallback yields a complete row even with no translations[].
//   - Every OTHER supported language is written ONLY when translations[] carries
//     a matching iso_639_1 entry, and uses ONLY that entry's OWN fields — it
//     does NOT fall back to root, because root here is localised to the CALL
//     lang (unlike RefreshSeriesAllLangs, whose root is pinned to en-US). An
//     empty translation field stays nil so the skip-both guard + repo COALESCE
//     preserve any prior value instead of poisoning the row with call-lang text
//     (the #973 "seasons RU-in-EN" class, aimed here at the en-US fallback tier).
//     Absent entry → the language is SKIPPED (row stays absent so the probe's
//     missing_episodes_lang verdict stays Stale and the per-lang async path can
//     fill it later).
//   - A both-empty projection is skipped (no content-less row bumps enriched_at).
//
// The stored `language` column is the SUPPORTED tag (e.g. "en-US"), matched to
// the call lang by primary subtag — so opening a season under ru-RU also lands
// the en-US row and vice-versa.
func buildSeasonTextWrites(
	seasonResp *tmdb.SeasonResponse,
	seriesID domain.SeriesID,
	seasonNumber int,
	callLang string,
	now time.Time,
) []series.SeasonText {
	trByLang := make(map[string]*tmdb.SeasonTranslation)
	if seasonResp.Translations != nil {
		for i := range seasonResp.Translations.Translations {
			t := &seasonResp.Translations.Translations[i]
			trByLang[shortLang(t.ISO6391)] = t
		}
	}

	writes := make([]series.SeasonText, 0, len(locale.SupportedUserLanguages))
	for _, l := range locale.SupportedUserLanguages {
		tr := trByLang[shortLang(l)]
		isCall := shortLang(l) == shortLang(callLang)
		if !isCall && tr == nil {
			// Non-call lang with no translation entry → skip (keep row absent).
			continue
		}

		// Root fallback is SAFE ONLY for the call-language row: GetSeason
		// localises the ROOT Name/Overview to the CALL lang, so seeding a
		// NON-call lang from root would store the call-lang text under the
		// wrong key (the #973 "seasons RU-in-EN" class — the en-US row is the
		// universal COALESCE fallback tier, so poisoning it is worst-case).
		// The call lang uses root as its source (tr may be nil → root-only, or
		// the matching entry); every other lang uses the translation entry's
		// OWN fields ONLY (empty → nil, letting the skip-both guard + repo
		// COALESCE handle absence).
		name, overview := "", ""
		if isCall {
			name, overview = seasonResp.Name, seasonResp.Overview
		}
		if tr != nil {
			if tr.Data.Name != "" {
				name = tr.Data.Name
			}
			if tr.Data.Overview != "" {
				overview = tr.Data.Overview
			}
		}

		nPtr := nonEmptyStringPtr(name)
		oPtr := nonEmptyStringPtr(overview)
		if nPtr == nil && oPtr == nil {
			continue // both empty → no content-less row
		}

		enrichedAt := now
		writes = append(writes, series.SeasonText{
			SeriesID:     seriesID,
			SeasonNumber: seasonNumber,
			Language:     l,
			Name:         nPtr,
			Overview:     oPtr,
			EnrichedAt:   &enrichedAt,
		})
	}
	return writes
}

// buildSeasonMediaTextWrites projects a GetSeason(+images) payload into one
// season_media_texts row per supported language (S-C2). Eager-resolves each
// poster hash via the resolver (media_assets pre-warm side-effect) — call it
// OUTSIDE the write tx.
//
// Row policy (mirrors buildSeasonTextWrites' anti-poison discipline):
//   - CALL language: poster = pickSeasonPosterForLang (short(lang) → agnostic →
//     "en"); if nil → root seasonResp.PosterPath (TMDB localises the root poster
//     to the call language). ALWAYS written when any poster exists.
//   - NON-call language: poster = pickSeasonPosterStrict (that EXACT language's
//     posters only — no agnostic/en/root fallback), so the en-US tier is never
//     poisoned by call-lang art. When nil the language is SKIPPED (row stays
//     absent; the per-lang async GetSeason path + reader en-US→canon fallback
//     cover it).
//   - A lang with no poster at all → skipped (no content-less row bumps
//     enriched_at). backdrop_* stay nil — TMDB season images are posters-only.
//
// The stored `language` column is the SUPPORTED tag (e.g. "en-US"), matched to
// the call lang by primary subtag.
func buildSeasonMediaTextWrites(
	ctx context.Context,
	resolver MediaResolver, // nil-OK
	seasonResp *tmdb.SeasonResponse,
	seriesID domain.SeriesID,
	seasonNumber int,
	callLang string,
	now time.Time,
) []series.SeasonMediaText {
	writes := make([]series.SeasonMediaText, 0, len(locale.SupportedUserLanguages))
	for _, l := range locale.SupportedUserLanguages {
		var posterPath *string
		if shortLang(l) == shortLang(callLang) {
			posterPath = pickSeasonPosterForLang(seasonResp.Images, l)
			if posterPath == nil {
				posterPath = nonEmptyStringPtr(seasonResp.PosterPath)
			}
		} else {
			posterPath = pickSeasonPosterStrict(seasonResp.Images, l)
		}
		if posterPath == nil {
			continue // no poster for this lang → keep row absent
		}

		var posterHash *string
		if resolver != nil {
			posterHash = resolver.Resolve(ctx, posterPath, "w342", "poster_w342")
		}

		enrichedAt := now
		writes = append(writes, series.SeasonMediaText{
			SeriesID:     seriesID,
			SeasonNumber: seasonNumber,
			Language:     l,
			PosterAsset:  posterPath,
			PosterHash:   posterHash,
			// BackdropAsset/BackdropHash intentionally nil (season images = posters only).
			EnrichedAt: &enrichedAt,
		})
	}
	return writes
}
