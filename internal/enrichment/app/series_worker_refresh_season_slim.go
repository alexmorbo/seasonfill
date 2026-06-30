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
// stub upsert; preserves season_id for episodes FK) → Episodes.BatchUpsert
// (in tx) → EpisodeTexts.Upsert per episode (in tx) → MarkSeasonEpisodesSynced
// UPDATE (in tx) → commit. tx rollback if any write fails; stamp is NEVER
// written without a successful episodes+texts UPSERT.
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
	seasonPayload := series.CanonSeason{
		SeriesID:     seriesID,
		SeasonNumber: seasonNumber,
		TMDBSeasonID: nonZeroIntPtrSlim(int(seasonResp.ID)),
		Name:         nonEmptyStringPtr(seasonResp.Name),
		Overview:     nonEmptyStringPtr(seasonResp.Overview),
		AirDate:      parseDateOrNilSlim(seasonResp.AirDate),
		PosterAsset:  nonEmptyStringPtr(seasonResp.PosterPath),
	}

	now := w.deps.Clock()
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
