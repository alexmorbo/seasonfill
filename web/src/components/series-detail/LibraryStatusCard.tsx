import { ArrowDown, CircleAlert, FolderInput, Inbox } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import type { LibraryStrip, DownloadChip, RecentEvent } from '@/api/seriesDetail';

export interface LibraryStatusCardProps {
  readonly library?: LibraryStrip | undefined;
  readonly download?: DownloadChip | undefined;
  readonly recent?: readonly RecentEvent[] | undefined;
  readonly className?: string | undefined;
}

function fmtBytes(n: number | undefined): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

function percent(on: number | undefined, total: number | undefined): number {
  if (!total || total <= 0) return 0;
  return Math.min(100, Math.max(0, Math.round(((on ?? 0) / total) * 100)));
}

function eventDotColor(type: string): string {
  switch (type) {
    case 'imported': return 'bg-ok';
    case 'grabbed': return 'bg-info';
    case 'failed': return 'bg-danger';
    default: return 'bg-tx-faint';
  }
}

export function LibraryStatusCard({ library, download, recent, className }: LibraryStatusCardProps) {
  const { t } = useTranslation();
  const onDisk = library?.episodes_on_disk ?? 0;
  const total = library?.episodes_total ?? 0;
  const missing = library?.missing_count ?? 0;
  const size = library?.size_on_disk_bytes ?? 0;
  const pct = percent(onDisk, total);
  const hasAnything = total > 0;
  const dominantQ = library?.dominant_quality && library.dominant_quality.length > 0
    ? library.dominant_quality : undefined;
  const recentEvents = (recent ?? []).slice(0, 3);

  return (
    <section
      data-testid="library-status-card"
      className={cn(
        'flex flex-col gap-3 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
        className,
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="text-[10.5px] font-bold uppercase tracking-wide text-tx-faint">
          {t('seriesDetail.library.label')}
        </div>
      </div>

      {!hasAnything ? (
        <div className="flex items-center gap-2 text-[13px] text-tx-muted">
          <Inbox className="w-4 h-4" aria-hidden="true" />
          {t('seriesDetail.library.nothingOnDisk')}
        </div>
      ) : (
        <>
          {/* Progress bar */}
          <div className="flex items-center gap-3">
            <div
              className="relative flex-1 h-1.5 rounded-full bg-bg-surface-2 overflow-hidden"
              role="progressbar"
              aria-valuenow={pct}
              aria-valuemin={0}
              aria-valuemax={100}
              data-testid="library-progress"
            >
              <div
                className="absolute inset-y-0 left-0 bg-accent rounded-full"
                style={{ width: `${pct}%` }}
              />
            </div>
            <span className="tabular-nums text-[12.5px] font-semibold text-tx-primary">{pct}%</span>
          </div>

          {/* Stat chips */}
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12px] text-tx-secondary">
            <span className="tabular-nums">
              {t('seriesDetail.library.onDiskCounts', { on: onDisk, total })}
            </span>
            <span aria-hidden="true" className="chip-dot bg-tx-faint" />
            <span className="tabular-nums">{fmtBytes(size)}</span>
            {dominantQ && (
              <>
                <span aria-hidden="true" className="chip-dot bg-tx-faint" />
                <span className="text-tx-muted">{dominantQ}</span>
              </>
            )}
            {missing > 0 && (
              <span
                data-testid="library-missing-chip"
                className="inline-flex items-center gap-1 rounded-full bg-danger-dim text-danger px-2 py-0.5 text-[11px] font-semibold"
              >
                <CircleAlert className="w-3 h-3" aria-hidden="true" />
                {t('seriesDetail.library.missing', { count: missing })}
              </span>
            )}
          </div>

          {/* Download chip (Sonarr queue) — minimal one-line variant */}
          {download && (
            <div
              data-testid="library-download"
              className="inline-flex items-center gap-1.5 rounded-md bg-info-dim text-info px-2 py-1 text-[12px] self-start"
            >
              {download.status === 'importing' ? (
                <FolderInput className="w-3.5 h-3.5" aria-hidden="true" />
              ) : (
                <ArrowDown className="w-3.5 h-3.5" aria-hidden="true" />
              )}
              <span className="text-tx-primary">
                {download.title ?? t('seriesDetail.library.downloadingFallback')}
              </span>
            </div>
          )}

          {/* Recent activity (≤3) */}
          {recentEvents.length > 0 && (
            <div
              data-testid="library-recent"
              className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-tx-muted pt-1 border-t border-border-faint/60"
            >
              <span className="text-[10px] font-bold uppercase tracking-wide text-tx-faint">
                {t('seriesDetail.library.recent')}
              </span>
              {recentEvents.map((ev, i) => (
                <span key={`${ev.event_type}-${ev.at}-${i}`} className="inline-flex items-center gap-1.5">
                  <span aria-hidden="true" className={cn('w-1.5 h-1.5 rounded-full', eventDotColor(ev.event_type ?? ''))} />
                  <span className="text-tx-secondary">
                    {t(`seriesDetail.library.event.${ev.event_type ?? 'unknown'}`, { defaultValue: ev.event_type ?? '' })}
                  </span>
                  {ev.subject && <span className="mono text-tx-muted">{ev.subject}</span>}
                  {ev.at && <span className="text-tx-faint">· {relativeTime(ev.at)}</span>}
                </span>
              ))}
            </div>
          )}
        </>
      )}
    </section>
  );
}
