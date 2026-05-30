export type DryRunChoice = 'auto' | 'on' | 'off';

export function dryRunFromWire(v: boolean | undefined | null): DryRunChoice {
  if (v === true) return 'on';
  if (v === false) return 'off';
  return 'auto';
}

export function dryRunToWire(c: DryRunChoice): boolean | undefined {
  if (c === 'on') return true;
  if (c === 'off') return false;
  return undefined;
}

export const FORM_DEFAULTS = {
  name: '',
  url: 'http://sonarr:8989',
  api_key: '',
  mode: 'auto' as const,
  dry_run: 'auto' as DryRunChoice,
  timeout_sec: 10,
  search_timeout_sec: 60,
  tags_mode: 'off' as const,
  tags_include: [] as string[],
  tags_exclude: [] as string[],
  search_require_all_aired: false,
  search_skip_specials: false,
  search_skip_anime: false,
  search_min_custom_format_score: 0,
  ranking_indexer_priority_enabled: false,
  ranking_origin_bonus: 0,
  rate_limit_rpm: 0,
  rate_limit_burst: 0,
  limits_scan_max_series: 0,
  limits_max_grabs_per_scan: 10,
  cooldown_mode: 'smart' as const,
  cooldown_series_after_grab_sec: 86400,
  cooldown_guid_after_failed_grab_sec: 259200,
  cooldown_guid_after_failed_import_sec: 172800,
  retry_max_attempts: 3,
  retry_initial_backoff_sec: 1,
  retry_max_backoff_sec: 30,
  health_recheck_auth_sec: 300,
  health_recheck_network_sec: 60,
};
