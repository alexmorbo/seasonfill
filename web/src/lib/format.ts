import i18n from '@/i18n';

const UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ['year', 31_536_000_000],
  ['month', 2_592_000_000],
  ['day', 86_400_000],
  ['hour', 3_600_000],
  ['minute', 60_000],
  ['second', 1_000],
];

function rtf(): Intl.RelativeTimeFormat {
  const lng = i18n.resolvedLanguage ?? 'en';
  return new Intl.RelativeTimeFormat(lng, { numeric: 'auto', style: 'short' });
}

export function relativeTime(input: string | number | Date | null | undefined): string {
  if (!input) return '—';
  const ts = typeof input === 'string' || typeof input === 'number' ? new Date(input) : input;
  const ms = ts.getTime();
  if (Number.isNaN(ms)) return '—';
  const diff = ms - Date.now();
  const fmt = rtf();
  for (const [unit, span] of UNITS) {
    if (Math.abs(diff) >= span || unit === 'second') return fmt.format(Math.round(diff / span), unit);
  }
  return '—';
}

export function durationMs(startIso?: string, endIso?: string): string {
  if (!startIso || !endIso) return '—';
  const ms = new Date(endIso).getTime() - new Date(startIso).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '—';
  if (ms < 1000) return `${ms}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m${s % 60 ? ` ${s % 60}s` : ''}`;
}
