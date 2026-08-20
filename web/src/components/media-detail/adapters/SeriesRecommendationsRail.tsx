import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { SeriesCard } from '@/components/series/SeriesCard';
import { MediaRecommendationsRail } from '@/components/media-detail/MediaRecommendationsRail';
import { useSeriesRecommendationsRail } from './useSeriesRecommendationsRail';
import type { RecommendationItem } from '../view-model';

export interface SeriesRecommendationsRailProps {
  readonly seriesId: number;
  readonly staleBadge?: ReactNode;
}

// U-4 sub-step B / §9 R1 fix — the review-mandated fallback: this component
// (not the page) owns `useSeriesRecommendationsRail`'s call site, so the
// hook's single `useEffect` run happens at the SAME mount timing the old
// `RecommendationsCarousel` had (mounted only inside the
// `detail.isSuccess`/`<MediaDetail>` branch, sentinel already in the DOM).
// Calling the hook at the SeriesDetail page's top level ran it BEFORE the
// success branch (and its sentinel `<section ref>`) ever mounted, so
// `useIsSectionVisible`'s one-shot effect saw `ref.current === null` and the
// IntersectionObserver was never attached — the rail would silently never
// fetch/render in a real browser. Mounting the hook here, inside
// `MediaDetail`'s success-only `recommendationsSlot`, restores the original
// working timing.
export function SeriesRecommendationsRail({ seriesId, staleBadge }: SeriesRecommendationsRailProps) {
  const { t } = useTranslation();
  const recsRail = useSeriesRecommendationsRail(seriesId);

  const renderCard = (rec: RecommendationItem, idx: number) => (
    <SeriesCard
      key={rec.series_id || rec.tmdb_series_id || rec.title || `idx-${idx}`}
      seriesId={rec.series_id}
      tmdbId={rec.tmdb_series_id}
      title={rec.title ?? ''}
      year={rec.year}
      posterAsset={rec.poster_asset}
      rating={rec.tmdb_rating}
      libraryBadge={rec.in_library ? 'inLibrary' : undefined}
      className="snap-start min-w-[124px] md:min-w-0"
    />
  );

  return (
    <MediaRecommendationsRail
      items={recsRail.items}
      isLoading={recsRail.isLoading}
      visible={recsRail.visible}
      sentinelRef={recsRail.sentinelRef}
      renderCard={renderCard}
      {...(staleBadge ? { staleBadge } : {})}
      label={t('seriesDetail.recommendations.label')}
      loadingLabel={t('seriesDetail.degraded.recommendations.loading')}
    />
  );
}
