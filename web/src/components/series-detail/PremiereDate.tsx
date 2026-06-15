import { useTranslation } from 'react-i18next';

export interface PremiereDateProps {
  /** ISO YYYY-MM-DD. Invalid/empty → returns null. */
  readonly iso: string | undefined;
}

/**
 * Locale-aware short-date renderer for the series' first-air date.
 * Avoids the timezone-aware `formatDate` helper in lib/timezone.tsx
 * because the premiere date is a calendar date (no time-of-day, no zone).
 * Uses Intl.DateTimeFormat directly; falls back to the raw ISO on Intl
 * errors (defensive — never crashes the rail card).
 */
export function PremiereDate({ iso }: PremiereDateProps) {
  const { i18n } = useTranslation();
  if (!iso) return null;
  // Parse YYYY-MM-DD as local-midnight to avoid timezone drift across
  // the date line. The Date constructor's "YYYY-MM-DD" branch is UTC
  // midnight, which can render the previous day in negative offsets.
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!m) return <>{iso}</>;
  const y = Number(m[1]);
  const mo = Number(m[2]) - 1;
  const d = Number(m[3]);
  if (!Number.isFinite(y) || !Number.isFinite(mo) || !Number.isFinite(d)) {
    return <>{iso}</>;
  }
  let text = iso;
  try {
    const locale = i18n.resolvedLanguage || 'en';
    const fmt = new Intl.DateTimeFormat(locale, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
    text = fmt.format(new Date(y, mo, d));
  } catch {
    // Fallback to raw ISO on error
  }
  return <>{text}</>;
}
