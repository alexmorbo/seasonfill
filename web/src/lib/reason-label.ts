import type { TFunction } from 'i18next';

const REDUNDANT_WITH_CATEGORY = new Set([
  'skip_sonarr_handles',
  'skip_all_complete',
  'skip_no_missing_episodes',
]);

export function resolveReasonLabel(
  reason: string | null | undefined,
  t: TFunction,
): string {
  if (!reason) return '—';
  if (REDUNDANT_WITH_CATEGORY.has(reason)) return '';
  return t(`reasons.${reason}`, { defaultValue: '—' });
}
