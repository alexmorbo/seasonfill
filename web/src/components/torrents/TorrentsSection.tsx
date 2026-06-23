import { useRef } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { TriangleAlert } from 'lucide-react';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { useQbitSettings } from '@/api/qbit';
import { useSeriesTorrents, useIsSectionVisible, type TorrentRow as TorrentRowDTO } from '@/api/seriesTorrents';
import { TorrentsTable } from './TorrentsTable';
import { TorrentCard } from './TorrentCard';
import { TorrentsEmptyState } from './TorrentsEmptyState';

export interface TorrentsSectionProps {
  readonly instance: string;
  readonly seriesId: number;
  readonly className?: string | undefined;
}

// Stale threshold: 20× the 3-second poll interval. Picked so the
// reconciler has 4 ticks before we cry wolf. Anything older counts
// as "qBit unreachable".
const STALE_THRESHOLD_MS = 60_000;

export function TorrentsSection({ instance, seriesId, className }: TorrentsSectionProps) {
  const { t } = useTranslation();
  const ref = useRef<HTMLElement | null>(null);
  const visible = useIsSectionVisible(ref);

  // Layer 1 — qBit configured? `useQbitSettings` returns `null` for
  // 404 (no row). If the row exists but `enabled=false`, the operator
  // explicitly turned the integration off — same outcome.
  const settings = useQbitSettings(instance);
  const qbitConfigured = settings.data != null && settings.data.enabled !== false;

  // Layer 2 — fetch + 3s poll, gated by visibility.
  const torrents = useSeriesTorrents({
    seriesId,
    visible,
    enabled: qbitConfigured,
  });

  // Section hidden entirely when qBit is not configured (PRD §4.5.3,
  // CD handoff Q1 — no upsell card, no anchor).
  if (settings.isFetched && !qbitConfigured) return null;
  // While qBit settings are still loading we keep the section out of
  // the DOM rather than flashing a skeleton — the sub-nav anchor
  // would otherwise pop in and out.
  if (settings.isPending) return null;

  const rows = (torrents.data?.torrents ?? []) as readonly TorrentRowDTO[];
  const syncedAt = torrents.data?.synced_at;
  const isStale = visible && syncedAt != null && isOlderThan(syncedAt, STALE_THRESHOLD_MS);

  // Empty-state branches (PRD §4.5):
  //   never       → torrents=[] AND qBit configured
  //   all-deleted → all rows have present=false
  const isNeverEmpty = rows.length === 0;
  const isAllDeleted = rows.length > 0 && rows.every((r) => r.present === false);

  // Aggregate copy for the header — count + per-state.
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
        <TorrentsEmptyState variant="never" />
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
            <TorrentsTable rows={rows} />
          </div>
          {/* Mobile cards */}
          <div className="md:hidden flex flex-col gap-2">
            {rows.map((r) => (
              <TorrentCard key={r.hash ?? `${r.name}-${r.added_on}`} row={r} />
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
