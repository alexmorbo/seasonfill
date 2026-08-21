import { useRef } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { TriangleAlert } from 'lucide-react';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { useQbitSettings } from '@/api/qbit';
import { useIsSectionVisible, type TorrentRow as TorrentRowDTO } from '@/api/seriesTorrents';
import { useMovieTorrents } from '@/api/movieTorrents';
import { TorrentsTable } from './TorrentsTable';
import { TorrentCard } from './TorrentCard';
import { TorrentsEmptyState } from './TorrentsEmptyState';

export interface MovieTorrentsSectionProps {
  readonly instance: string;
  readonly tmdbId: number;
  readonly className?: string | undefined;
}

// Kept in lockstep with TorrentsSection.tsx's STALE_THRESHOLD_MS — see
// that file for the rationale (20x the 3s poll interval).
const STALE_THRESHOLD_MS = 60_000;

// MovieTorrentsSection — B1.5 (ADR-0023) movie twin of TorrentsSection.tsx.
//
// A SEPARATE component rather than a generalized/parametrised
// TorrentsSection: the frozen SeriesDetail test suite renders
// `<TorrentsSection instance seriesId>` directly (SeriesDetail.tsx:355)
// and TorrentsSection.test.tsx exercises its exact prop surface; keeping
// this file distinct means TorrentsSection.tsx needs ZERO edits, so both
// stay byte-identical to before this story. The two components share
// every presentational leaf (TorrentsTable/TorrentCard/TorrentsEmptyState/
// TorrentStateChip/TorrentActions/...) — only the data hook
// (useMovieTorrents vs useSeriesTorrents) and the "never" empty-state copy
// (Radarr/movie wording via TorrentsEmptyState's `i18nBase`) differ.
export function MovieTorrentsSection({ instance, tmdbId, className }: MovieTorrentsSectionProps) {
  const { t } = useTranslation();
  const ref = useRef<HTMLElement | null>(null);
  const visible = useIsSectionVisible(ref);

  // Layer 1 — qBit configured? Same 404-tolerant contract as the series
  // panel (see TorrentsSection.tsx).
  const settings = useQbitSettings(instance);
  const qbitConfigured = settings.data != null && settings.data.enabled !== false;

  // Layer 2 — fetch + 3s poll, gated by visibility.
  const torrents = useMovieTorrents({
    tmdbId,
    visible,
    enabled: qbitConfigured,
  });

  if (settings.isFetched && !qbitConfigured) return null;
  if (settings.isPending) return null;

  const rows = (torrents.data?.torrents ?? []) as readonly TorrentRowDTO[];
  const syncedAt = torrents.data?.synced_at;
  const isStale = visible && syncedAt != null && isOlderThan(syncedAt, STALE_THRESHOLD_MS);

  const isNeverEmpty = rows.length === 0;
  const isAllDeleted = rows.length > 0 && rows.every((r) => r.present === false);

  const totalSize = rows.reduce((acc, r) => acc + (r.size_bytes ?? 0), 0);
  const stateCounts = rows.reduce<Record<string, number>>((acc, r) => {
    if (r.present === false) {
      acc.deleted = (acc.deleted ?? 0) + 1;
    } else {
      const k = r.state_group ?? 'unknown';
      acc[k] = (acc[k] ?? 0) + 1;
    }
    return acc;
  }, {});

  return (
    <section
      ref={ref}
      data-testid="torrents-section"
      data-visible={visible ? 'true' : 'false'}
      data-stale={isStale ? 'true' : 'false'}
      className={cn(
        'flex flex-col gap-3 rounded-lg border border-border-faint bg-bg-surface/40 px-3 py-3',
        className,
      )}
      id="torrents"
    >
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <h2 className="text-[12px] font-bold uppercase tracking-wide text-tx-muted">
            {t('seriesDetail.torrents.label')}
            <span className="ml-1 text-tx-secondary tabular-nums">({rows.length})</span>
          </h2>
          {rows.length > 0 && (
            <span className="text-[11px] text-tx-muted tabular-nums">
              {summarize(t, stateCounts, totalSize)}
            </span>
          )}
        </div>
        {syncedAt && !isStale && (
          <span data-testid="torrents-synced" className="text-[10.5px] text-tx-faint tabular-nums">
            {t('seriesDetail.torrents.syncedAgo', { time: relativeTime(syncedAt) })}
          </span>
        )}
      </header>

      {isStale && (
        <div
          data-testid="torrents-stale-banner"
          className="flex items-center gap-2 rounded-md border border-warn/45 bg-warn-dim text-warn px-3 py-1.5 text-[12px]"
          role="status"
        >
          <TriangleAlert className="w-3.5 h-3.5" aria-hidden="true" />
          <span>
            {t('seriesDetail.torrents.stale.banner', {
              time: syncedAt ? relativeTime(syncedAt) : '',
            })}
          </span>
        </div>
      )}

      {isNeverEmpty && !torrents.isPending && (
        <TorrentsEmptyState variant="never" i18nBase="movieDetail.torrents" />
      )}

      {isAllDeleted && (
        <div data-testid="torrents-all-deleted-note" className="text-[11.5px] text-tx-muted italic">
          {t('seriesDetail.torrents.allDeletedNote', { count: rows.length })}
        </div>
      )}

      {rows.length > 0 && (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <TorrentsTable rows={rows} instance={instance} />
          </div>
          {/* Mobile cards */}
          <div className="md:hidden flex flex-col gap-2">
            {rows.map((r) => (
              <TorrentCard key={r.hash ?? `${r.name}-${r.added_on}`} row={r} instance={instance} />
            ))}
          </div>
        </>
      )}
    </section>
  );
}

function summarize(
  t: TFunction,
  counts: Record<string, number>,
  totalSize: number,
): string {
  const parts: string[] = [];
  const order = ['downloading', 'seeding', 'stalled', 'queued', 'paused', 'checking', 'error', 'unknown', 'deleted'];
  for (const k of order) {
    const n = counts[k];
    if (n) parts.push(t(`seriesDetail.torrents.summary.${k}`, { count: n }));
  }
  const size = humanSize(totalSize);
  return [parts.join(' · '), size].filter(Boolean).join(' · ');
}

function isOlderThan(iso: string, ms: number): boolean {
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return false;
  return Date.now() - ts > ms;
}

function humanSize(n: number): string {
  if (!n || n <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}
