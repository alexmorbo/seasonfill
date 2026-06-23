import { Star } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import type { RatingScore } from '@/api/series';
import { StaleBadge } from './StaleBadge';
import { Skeleton } from '@/components/ui/skeleton';

export interface RatingDuoProps {
  readonly tmdb?: RatingScore | undefined;
  readonly imdb?: RatingScore | undefined;
  readonly tmdbStaleAt?: string | undefined;
  readonly imdbStaleAt?: string | undefined;
  // Story 495 / N-1e (B-20): render an IMDb skeleton chip while OMDb
  // enrichment is in flight (`degraded[]` carries `'omdb'` AND no
  // rating yet).
  readonly imdbLoading?: boolean | undefined;
  readonly className?: string | undefined;
}

// eslint-disable-next-line react-refresh/only-export-components
export function humanizeVotes(n: number | undefined): string {
  if (!n || n <= 0) return '';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1).replace(/\.0$/, '')}k`;
  return String(n);
}

function ratingValid(r: RatingScore | undefined): r is RatingScore {
  return Boolean(r) && typeof r?.score === 'number' && r.score > 0;
}

export function RatingDuo({ tmdb, imdb, tmdbStaleAt, imdbStaleAt, imdbLoading, className }: RatingDuoProps) {
  const { t } = useTranslation();
  const showTmdb = ratingValid(tmdb);
  const showImdb = ratingValid(imdb);
  const showImdbLoading = Boolean(imdbLoading) && !showImdb;
  if (!showTmdb && !showImdb && !showImdbLoading) return null;
  return (
    <div className={cn('flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12.5px]', className)}>
      {showTmdb && (
        <span data-testid="rating-tmdb" className="inline-flex items-center gap-1.5">
          <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
            {t('seriesDetail.ratings.tmdb')}
          </span>
          <Star className="w-3.5 h-3.5 text-warn" aria-hidden="true" fill="currentColor" />
          <span className="font-semibold text-tx-primary tabular-nums">{tmdb!.score!.toFixed(1)}</span>
          {tmdb!.votes !== undefined && tmdb!.votes > 0 && (
            <span className="text-tx-faint tabular-nums">· {humanizeVotes(tmdb!.votes)}</span>
          )}
          {tmdbStaleAt && <StaleBadge asOf={tmdbStaleAt} source="tmdb" />}
        </span>
      )}
      {showImdb && (
        <span data-testid="rating-imdb" className="inline-flex items-center gap-1.5">
          <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
            {t('seriesDetail.ratings.imdb')}
          </span>
          <Star className="w-3.5 h-3.5 text-warn" aria-hidden="true" fill="currentColor" />
          <span className="font-semibold text-tx-primary tabular-nums">{imdb!.score!.toFixed(1)}</span>
          {imdb!.votes !== undefined && imdb!.votes > 0 && (
            <span className="text-tx-faint tabular-nums">· {humanizeVotes(imdb!.votes)}</span>
          )}
          {imdbStaleAt && <StaleBadge asOf={imdbStaleAt} source="omdb" />}
        </span>
      )}
      {showImdbLoading && (
        <span
          data-testid="imdb-rating-loading"
          className="inline-flex items-center gap-1.5"
        >
          <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
            {t('seriesDetail.ratings.imdb')}
          </span>
          <Star className="w-3.5 h-3.5 text-tx-faint" aria-hidden="true" />
          <Skeleton className="h-3 w-10" />
          <span className="text-[10.5px] text-tx-faint">
            {t('seriesDetail.degraded.imdb.loading')}
          </span>
        </span>
      )}
    </div>
  );
}
