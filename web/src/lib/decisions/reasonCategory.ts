// Reason → UX category map for the F7 Decisions accordion. The
// design's "Результат" dropdown has 5 buckets:
//
//   done     — operator acted (grab dispatched)
//   none     — operator-relevant "nothing to grab" (incl. errors)
//   blocked  — cooldown / policy block
//   sonarr   — Sonarr's own scheduler is the right tool
//   ok       — all complete, no work to do
//
// The wire `decision`/`reason` strings carry finer detail; this map
// reduces them to the 5-bucket UX vocabulary. The fallback for
// unknown reason strings is `none` (conservative — surfaces the row
// so the operator notices instead of silently bucketing it as `ok`).

export type ReasonCategoryKey = 'done' | 'none' | 'blocked' | 'sonarr' | 'ok';

export const REASON_CATEGORY: Record<string, ReasonCategoryKey> = {
  // === done (action taken) ===
  grab_selected:         'done',
  grab_selected_dry_run: 'done',
  upgrade_available:     'done',

  // === none (no candidates / no candidates passed filter) ===
  skip_no_candidates_after_filter:  'none',
  skip_no_releases_returned:        'none',
  no_candidates:                    'none',
  nothing_above_threshold:          'none',
  release_covers_no_missing_episodes: 'none',
  quality_not_in_profile:           'none',
  would_downgrade_existing_quality: 'none',
  rejection_not_in_safe_list:       'none',
  custom_format_score_below_minimum: 'none',
  release_partial_and_require_all_aired: 'none',
  release_in_guid_cooldown:         'none',
  unknown_series_mapping:           'none',
  // error_* reasons are surfaced as `none` (red category dot drives
  // the visual differentiation; `none` keeps the dropdown count
  // honest — "Ничего не найдено" includes error events because the
  // operator's question is identical: "Why nothing in season X?").
  error_fetch_releases:        'none',
  error_fetch_episodes:        'none',
  error_fetch_episode_files:   'none',
  error_fetch_quality_profile: 'none',
  failed_grab:                 'none',
  failed_release_fetch:        'none',
  failed_indexer:              'none',

  // === blocked (cooldown / policy) ===
  blocked_cooldown:       'blocked',
  skip_series_in_cooldown: 'blocked',
  skip_in_cooldown:        'blocked',
  blocked_health:          'blocked',
  skip_origin_blocked:     'blocked',
  skip_tag_excluded:       'blocked',
  skip_max_grabs_per_scan_reached: 'blocked',
  skip_max_grabs_reached:           'blocked',

  // === sonarr (Sonarr handles this case itself) ===
  skip_unmonitored_season: 'sonarr',
  skip_specials_season:    'sonarr',
  skip_specials_filtered:  'sonarr',
  skip_anime_series:       'sonarr',
  skip_anime_filtered:     'sonarr',
  skip_all_episodes_missing: 'sonarr',
  skip_not_all_aired:      'sonarr',
  skip_already_queued:     'sonarr',
  skip_tag_required:       'sonarr',
  sonarr_handles:          'sonarr',

  // === ok (no work) ===
  skip_no_missing_episodes: 'ok',
  skip_no_missing:          'ok',
  already_optimal:          'ok',
  all_complete:             'ok',
};

export function reasonCategory(reason: string | undefined | null): ReasonCategoryKey {
  if (!reason) return 'none';
  return REASON_CATEGORY[reason] ?? 'none';
}

// UI-side category dropdown options — order matches the design
// reference. `all` is the implicit "no filter" value; selecting any
// other value pushes the wire-side `category` filter (client-side
// reduction; see lib/api/decisions.ts useDecisionsList select).
export const REASON_CATEGORY_OPTIONS: readonly ReasonCategoryKey[] = [
  'done', 'none', 'blocked', 'sonarr', 'ok',
] as const;

// Tailwind class bundles for the colored dot in front of each option
// in the dropdown. Mirrors the design's `.d` swatch:
//   done    → info (accent blue)
//   none    → warn
//   blocked → neutral
//   sonarr  → info-dim (info @ 70% opacity)
//   ok      → ok (success green)
export const REASON_CATEGORY_DOT_CLASS: Record<ReasonCategoryKey, string> = {
  done:    'bg-status-info',
  none:    'bg-status-warning',
  blocked: 'bg-tx-faint',
  sonarr:  'bg-status-info/70',
  ok:      'bg-status-success',
};

// Decision.category (wire) → UI category bucket. Used both by
// `useDecisionsList` client-side filter and by `DecisionsSeriesRow`
// status pill (053b). The wire enum already aligns mostly 1:1 with
// the UI buckets; `error` collapses into `none` for the same reason
// error_* reasons do (operator's question is "why no work?").
export function categoryToBucket(
  category: string | null | undefined,
): ReasonCategoryKey {
  switch (category) {
    case 'action_taken':   return 'done';
    case 'nothing_found':  return 'none';
    case 'error':          return 'none';
    case 'blocked':        return 'blocked';
    case 'sonarr_handles': return 'sonarr';
    case 'all_complete':   return 'ok';
    default:               return 'none';
  }
}
