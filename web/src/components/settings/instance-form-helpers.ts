export type DryRunChoice = 'auto' | 'on' | 'off';

// ADR-0023 A3b — Radarr's v3 minimumAvailability enum, in Radarr's own
// case-sensitive wire spelling. Shared by the instance form's zod schema and
// the detail→form seeding coercion.
export const MIN_AVAILABILITY_VALUES = ['announced', 'inCinemas', 'released'] as const;
export type MinAvailabilityValue = (typeof MIN_AVAILABILITY_VALUES)[number];

export function coerceMinAvailability(
  v: string | null | undefined,
): MinAvailabilityValue | null {
  return (MIN_AVAILABILITY_VALUES as readonly string[]).includes(v ?? '')
    ? (v as MinAvailabilityValue)
    : null;
}

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
  // Ф6-R-6b: arr kind. New instances default to sonarr; immutable after
  // creation (the edit dialog renders a read-only badge, never the selector).
  type: 'sonarr' as 'sonarr' | 'radarr',
  url: 'http://sonarr:8989',
  api_key: '',
  // 041h-1: Optional browser-facing URL. Empty string in the form ↔ omitted
  // on wire. Backend rejects '' with INVALID_INSTANCE_PUBLIC_URL — the form
  // must NEVER send '' for either of these two optional URL fields.
  public_url: '',
  // Backend default is true (041c-2 migration). A fresh-form operator who
  // never touches this switch creates instances with the reconciler ON,
  // matching the migration behaviour for existing rows.
  webhook_install_enabled: true,
  // Sibling of public_url, identical empty-string-vs-omit rule.
  webhook_url_override: '',
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
  // ADR-0009 S7: per-instance Add-to-Sonarr defaults. null = not set →
  // omitted on the wire (BE stores NULL). Seeded from detail on edit;
  // dropdowns only become editable after a successful in-dialog Test.
  default_quality_profile_id: null as number | null,
  default_root_folder_path: null as string | null,
  // ADR-0023 A3b: radarr-only add-movie default. null = not set → omitted on
  // the wire → Radarr's own default ("released") applies at add time. The
  // control is only rendered for type='radarr'.
  default_minimum_availability: null as MinAvailabilityValue | null,
};

// 057b1: qBittorrent / Watchdog defaults lifted from the old
// WatchdogTab.tsx. Names are `qbit_`-prefixed so they live in the
// same RHF FormValues as instance config. Defaults mirror 039d
// validation bounds.
//
// 083 / F-P2-1: `qbit_public_url` is the optional browser-reachable
// URL. Empty = "use qbit_url (or hide GrabDrawer link if internal)".
export const WATCHDOG_DEFAULTS = {
  qbit_url: 'http://qbittorrent:8080',
  qbit_public_url: '',
  qbit_username: '',
  qbit_password: '',
  qbit_category: 'sonarr',
  qbit_poll_interval_minutes: 30,
  qbit_regrab_cooldown_hours: 120,
  qbit_max_consecutive_no_better: 3,
  qbit_custom_unregistered_msgs: [] as string[],
  qbit_enabled: false,
} as const;
