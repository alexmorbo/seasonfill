import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface RatingsStripProps {
  readonly rtRating?: number | undefined;
  readonly metacritic?: number | undefined;
  readonly omdbDegraded?: boolean | undefined;
  readonly className?: string | undefined;
}

// RatingsStrip — Story 1039. Minimal placeholder surface for OMDb's
// Rotten Tomatoes / Metacritic scores, rendered next to AwardsBlock in
// the same aside column. Full visual redesign (unified iconography
// alongside TMDB/IMDb in the hero) is deferred to the design wave —
// this is deliberately a compact inline strip, not a new heavy
// component. Mirrors AwardsBlock's degraded-hide convention: both
// scores come from the same OMDb sync, so a stale/degraded OMDb state
// hides the strip exactly like it hides Awards.
export function RatingsStrip({
  rtRating,
  metacritic,
  omdbDegraded,
  className,
}: RatingsStripProps) {
  const { t } = useTranslation();

  if (omdbDegraded) return null;
  if (rtRating === undefined && metacritic === undefined) return null;

  return (
    <div
      data-testid="ratings-strip"
      className={cn('flex items-center gap-3 text-[12.5px] text-tx-secondary', className)}
    >
      {rtRating !== undefined && (
        <span
          data-testid="rating-rt"
          className="inline-flex items-center gap-1 font-semibold text-tx-primary tabular-nums"
        >
          <span aria-hidden="true">{'\u{1F345}'}</span>{' '}
          {rtRating}%
        </span>
      )}
      {metacritic !== undefined && (
        <span
          data-testid="rating-mc"
          className="inline-flex items-center gap-1 font-semibold text-tx-primary tabular-nums"
        >
          <span
            className="text-[10px] font-bold tracking-wide uppercase text-tx-faint"
            aria-hidden="true"
          >
            {t('seriesDetail.ratings.metacritic')}
          </span>{' '}
          {metacritic}
        </span>
      )}
    </div>
  );
}
