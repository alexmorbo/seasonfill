import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { mediaUrl, parseStatus } from '@/api/series';
import { StatusPill } from '@/components/series-detail/StatusPill';

export interface CompactHeroProps {
  readonly title: string | undefined;
  readonly posterAsset: string | undefined;
  // Movies have no TMDB status/crew vocabulary on this page (movie statuses
  // use a different i18n map than the series-only parseStatus()/StatusPill
  // vocabulary, and movies have no crew list here) — both undefined hides
  // the corresponding segment instead of misrendering "Unknown" / "0 crew".
  readonly status?: string | undefined;
  readonly yearStart: number | undefined;
  readonly yearEnd: number | undefined;
  readonly castCount: number;
  readonly crewCount?: number | undefined;
  readonly className?: string | undefined;
}

function formatYearRange(start: number | undefined, end: number | undefined): string {
  if (!start && !end) return '';
  if (start && end && start !== end) return `${start}–${end}`;
  if (start && end && start === end) return `${start}`;
  if (start && !end) return `${start}–`;
  return `${end ?? ''}`;
}

export function CompactHero({
  title,
  posterAsset,
  status,
  yearStart,
  yearEnd,
  castCount,
  crewCount,
  className,
}: CompactHeroProps) {
  const { t } = useTranslation();
  const poster = mediaUrl(posterAsset);
  const parsed = status !== undefined ? parseStatus(status) : undefined;
  const years = formatYearRange(yearStart, yearEnd);

  return (
    <header
      data-testid="cast-compact-hero"
      className={cn(
        'flex items-stretch gap-3 rounded-xl border border-border-faint bg-bg-surface p-3 md:p-4',
        className,
      )}
    >
      <div
        className="shrink-0 w-[60px] h-[90px] rounded-md overflow-hidden border border-border-subtle bg-bg-surface-2"
        aria-hidden="true"
      >
        {poster && (
          <img
            src={poster}
            alt=""
            loading="lazy"
            decoding="async"
            className="w-full h-full object-cover"
          />
        )}
      </div>

      <div className="flex flex-col justify-center min-w-0 gap-1.5">
        <div className="flex items-center gap-2 flex-wrap">
          <h1
            data-testid="cast-page-title"
            className="text-[15.5px] font-semibold text-tx-primary truncate max-w-[420px]"
          >
            {title ?? ''}
          </h1>
          {years && (
            <span className="text-[12px] text-tx-muted tabular-nums">{years}</span>
          )}
          {parsed !== undefined && <StatusPill status={parsed} />}
        </div>
        <div className="text-[11.5px] text-tx-faint tabular-nums" data-testid="cast-counts">
          <span>{t('seriesDetail.castPage.counts.cast', { count: castCount })}</span>
          {crewCount !== undefined && (
            <>
              <span aria-hidden="true"> · </span>
              <span>{t('seriesDetail.castPage.counts.crew', { count: crewCount })}</span>
            </>
          )}
        </div>
      </div>
    </header>
  );
}
