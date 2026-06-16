import { AlertTriangle, ArrowDown, FolderInput, Inbox, PieChart } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import type { LibraryStrip, DownloadChip } from '@/api/seriesDetail';

export interface HeroLibraryStripProps {
  readonly library?: LibraryStrip | undefined;
  readonly download?: DownloadChip | undefined;
  /** "dark" styles chips for use over the hero scrim. "light" styles for the
   *  page body. Defaults to "dark" because the primary use is in-hero. */
  readonly tone?: 'dark' | 'light';
  readonly onDownloadClick?: () => void;
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

export function HeroLibraryStrip({
  library, download, tone = 'dark', onDownloadClick, className,
}: HeroLibraryStripProps) {
  const { t } = useTranslation();
  const total = library?.episodes_total ?? 0;
  const aired = library?.episodes_aired ?? 0;
  const onDisk = library?.episodes_on_disk ?? 0;
  const missing = library?.missing_count ?? 0;
  const size = library?.size_on_disk_bytes ?? 0;
  // Story 376: prefer airedEpisodeCount as the denominator so unaired
  // future episodes don't depress the on-disk headline. Backward-compat
  // fallback to episodes_total when aired is 0 (legacy cached rows).
  const denominator = aired > 0 ? aired : total;
  const pct = percent(onDisk, denominator);
  const hasAnything = total > 0;

  // Tone-driven chip palette. "dark" — light-on-dark for use over the
  // bleed-hero scrim. "light" — uses the body surface tokens.
  const chipBase = tone === 'dark'
    ? 'bg-white/[0.08] border-white/[0.14] text-white/90'
    : 'bg-bg-surface-2 border-border-faint text-tx-secondary';
  const capColor = tone === 'dark' ? 'text-white/55' : 'text-tx-faint';

  return (
    <div
      data-testid="hero-library-strip"
      data-tone={tone}
      className={cn(
        'flex flex-wrap items-center gap-2 pt-3 mt-3 border-t',
        tone === 'dark' ? 'border-white/[0.12]' : 'border-border-faint',
        className,
      )}
    >
      <span className={cn('text-[10px] font-semibold uppercase tracking-[0.1em]', capColor)}>
        {t('seriesDetail.library.cap')}
      </span>

      {!hasAnything ? (
        <span className={cn(
          'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px]',
          chipBase,
        )} data-testid="hero-library-empty">
          <Inbox className="w-3 h-3" aria-hidden="true" />
          {t('seriesDetail.library.nothingOnDisk')}
        </span>
      ) : (
        <>
          <span
            data-testid="hero-library-percent"
            className={cn(
              'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px] font-medium border',
              chipBase,
            )}
          >
            <PieChart className="w-3 h-3" aria-hidden="true" />
            {pct}%
          </span>
          <span className={cn(
            'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px] font-mono border tabular-nums',
            chipBase,
          )} data-testid="hero-library-counts">
            {onDisk}/{denominator}
          </span>
          <span className={cn(
            'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px] font-mono border tabular-nums',
            chipBase,
          )} data-testid="hero-library-size">
            {fmtBytes(size)}
          </span>
          {missing > 0 && (
            <span
              data-testid="hero-library-missing"
              className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px] font-medium border bg-warn-dim/30 border-warn/40 text-warn"
            >
              <AlertTriangle className="w-3 h-3" aria-hidden="true" />
              {t('seriesDetail.library.missing', { count: missing })}
            </span>
          )}
          {download && (
            <button
              type="button"
              onClick={onDownloadClick}
              data-testid="hero-library-download"
              className={cn(
                'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px] border cursor-pointer hover:brightness-110 transition',
                chipBase,
              )}
            >
              {download.status === 'importing' ? (
                <FolderInput className="w-3 h-3" aria-hidden="true" />
              ) : (
                <ArrowDown className="w-3 h-3" aria-hidden="true" />
              )}
              <span className="font-medium">
                {download.title ?? t('seriesDetail.library.downloadShort')}
              </span>
              <span aria-hidden="true">→</span>
            </button>
          )}
        </>
      )}
    </div>
  );
}
