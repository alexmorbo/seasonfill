import i18n from '@/i18n';

const BYTES_PER_GB = 1024 ** 3;
const BYTES_PER_MB = 1024 ** 2;

export function formatSize(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return '—';
  const lng = i18n.resolvedLanguage ?? 'en';
  if (bytes >= BYTES_PER_GB) {
    const gb = bytes / BYTES_PER_GB;
    return new Intl.NumberFormat(lng, { maximumFractionDigits: 1 }).format(gb) + ' GB';
  }
  const mb = bytes / BYTES_PER_MB;
  return new Intl.NumberFormat(lng, { maximumFractionDigits: 0 }).format(mb) + ' MB';
}

// Format "S05 · E1–10", "S05 · E1–6 / 10", "S05 · E10"
export function formatEpisodeRange(
  season: number | undefined,
  episodes: number[] | undefined,
  totalInSeason: number | undefined,
): string {
  const s = `S${String(season ?? 0).padStart(2, '0')}`;
  if (!episodes || episodes.length === 0) return s;
  const sorted = [...episodes].sort((a, b) => a - b);
  const first = sorted[0]!;
  const last = sorted[sorted.length - 1]!;
  const isSingleEpisode = first === last;
  const range = isSingleEpisode ? `E${first}` : `E${first}–${last}`;
  const totalSuffix =
    !isSingleEpisode && totalInSeason !== undefined && episodes.length < totalInSeason
      ? ` / ${totalInSeason}`
      : '';
  return `${s} · ${range}${totalSuffix}`;
}

// "41с" (ru) / "41s" (en) for short durations; "1м 22с" / "1m 22s"
export function formatImportDuration(
  grabbedAt: string | null | undefined,
  importedAt: string | null | undefined,
): string {
  if (!grabbedAt || !importedAt) return '';
  const ms = new Date(importedAt).getTime() - new Date(grabbedAt).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '';
  const lng = i18n.resolvedLanguage ?? 'en';
  const sShort = lng.startsWith('ru') ? 'с' : 's';
  const mShort = lng.startsWith('ru') ? 'м' : 'm';
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return `${secs}${sShort}`;
  const m = Math.floor(secs / 60);
  const remS = secs % 60;
  return remS === 0 ? `${m}${mShort}` : `${m}${mShort} ${remS}${sShort}`;
}
