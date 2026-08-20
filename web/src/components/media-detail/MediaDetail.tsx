import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Skeleton } from '@/components/ui/skeleton';
import { useFormatDate } from '@/lib/timezone';
import type { DegradedSource } from '@/api/series';
import { DegradedChip } from '@/components/series-detail/DegradedChip';
import { OverviewGrid } from '@/components/series-detail/OverviewGrid';
import { LanguageFallbackTag } from '@/components/series-detail/LanguageFallbackTag';
import { ExternalLinksFooter } from '@/components/series-detail/ExternalLinksFooter';
import { StaleBadge } from '@/components/series-detail/StaleBadge';
import { MediaHero, type MediaHeroProps } from './MediaHero';
import { MediaCastStrip } from './MediaCastStrip';
import { MediaRatingsSection } from './MediaRatingsSection';
import { MediaRailCard } from './MediaRailCard';
import { MediaRecommendationsRail } from './MediaRecommendationsRail';
import type { MediaDetailVM } from './view-model';

export interface MediaDetailProps {
  readonly vm: MediaDetailVM;
  /** Section slots owned by the type-specific adapter (series: next-episode
   *  card + library strip; movie: compact collection card in `nextCard`
   *  when the movie belongs to a TMDB collection, else omitted). */
  readonly heroExtras?: MediaHeroProps['heroExtras'];
  /** Series-only sections (RecentStrip + Torrents + SeasonsAccordion) that
   *  render between the overview grid and the collection/recommendations
   *  tail — inert (undefined) for the movie page. */
  readonly belowGrid?: ReactNode;
  /**
   * U-4 sub-step B / §9 R1 fix — when provided, rendered INSTEAD of the
   * `vm.recommendations`-driven `<MediaRecommendationsRail>` below. Lets the
   * type-specific adapter own the recommendations hook's call site (so its
   * mount timing matches the pre-U-4 in-success-branch-only component),
   * while movie/U-6 keeps using the `vm.recommendations` fallback path when
   * this prop is omitted.
   */
  readonly recommendationsSlot?: ReactNode;
}

/**
 * MediaDetail — the shared media-type-parametrised detail-page scaffold
 * (U-4). Reproduces `SeriesDetail.tsx`'s pre-U-4 `detail.isSuccess` render
 * order EXACTLY (§3.6): hero → degraded chip → overview grid (text/cast/
 * ratings left, rail right) → belowGrid slot → collection slot →
 * recommendations → external links → synced footer.
 */
export function MediaDetail({ vm, heroExtras, belowGrid, recommendationsSlot }: MediaDetailProps) {
  const { t } = useTranslation();
  const fmt = useFormatDate();

  const tmdbSeriesDegraded = vm.degraded.includes('tmdb_series');
  const omdbDegraded = vm.degraded.includes('omdb');

  return (
    <>
      <MediaHero vm={vm} heroExtras={heroExtras} />

      {vm.degraded.length > 0 && (
        <div className="-mt-2 flex justify-end">
          <DegradedChip sources={vm.degraded as readonly DegradedSource[]} />
        </div>
      )}

      <section
        data-testid="overview-section"
        className="relative z-[2] rounded-md"
      >
        <OverviewGrid
          left={
            <>
              <div className="flex flex-col gap-3 min-w-0">
                <div className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint [text-shadow:0_1px_2px_oklch(0_0_0/.55)]">
                  {vm.overview.label}
                  <LanguageFallbackTag
                    contentLang={vm.overview.contentLang}
                    {...(vm.overview.requestedLang ? { requestedLang: vm.overview.requestedLang } : {})}
                    testid="overview-lang-fallback"
                  />
                </div>
                {vm.overview.loading && (
                  <div
                    data-testid="overview-skeleton"
                    className="flex flex-col gap-1.5 max-w-[64ch]"
                  >
                    <Skeleton className="h-3 w-full" />
                    <Skeleton className="h-3 w-[92%]" />
                    <Skeleton className="h-3 w-[78%]" />
                  </div>
                )}
                <p
                  data-testid="overview-text"
                  className="text-[13.5px] leading-relaxed text-tx-primary whitespace-pre-line max-w-[64ch] [text-shadow:0_1px_2px_oklch(0_0_0/.55)]"
                >
                  {vm.overview.text}
                </p>
              </div>
              {!vm.sonarrOnly && (
                <MediaCastStrip
                  castHref={vm.cast.href}
                  seriesId={vm.cast.mediaId}
                  cast={vm.cast.members}
                  {...(vm.cast.limit ? { limit: vm.cast.limit } : {})}
                  {...(vm.cast.loading ? { tmdbPersonDegraded: true } : {})}
                />
              )}
              {/* Canonical ratings surface (SWR /ratings) — TMDB ★, IMDb,
                  OMDb content-rating + awards. Self-hides when no source
                  carries a value. This is a DISTINCT resolution from the
                  hero's `vm.ratings.tmdb`/`imdb` (which fall back to the
                  skeleton rating for instant paint) — see the
                  `sectionTmdb*`/`sectionImdb*` doc comment on `MediaRatings`. */}
              <MediaRatingsSection
                tmdbRating={vm.ratings.sectionTmdbRating}
                tmdbVotes={vm.ratings.sectionTmdbVotes}
                imdbRating={vm.ratings.sectionImdbRating}
                imdbVotes={vm.ratings.sectionImdbVotes}
                rated={vm.ratings.rated}
                awards={vm.ratings.awards}
              />
            </>
          }
          right={<MediaRailCard facts={vm.sidebarFacts} keywords={vm.keywords} />}
        />
      </section>

      {belowGrid}

      {vm.collection?.node}

      {recommendationsSlot ?? (
        <MediaRecommendationsRail
          items={vm.recommendations.items}
          isLoading={vm.recommendations.isLoading}
          visible={vm.recommendations.visible}
          sentinelRef={vm.recommendations.sentinelRef}
          renderCard={vm.recommendations.renderCard}
          {...(vm.recommendations.staleBadge ? { staleBadge: vm.recommendations.staleBadge } : {})}
          label={t('seriesDetail.recommendations.label')}
          loadingLabel={t('seriesDetail.degraded.recommendations.loading')}
        />
      )}

      <ExternalLinksFooter links={vm.externalLinks} />

      {vm.syncedAt && (
        <div className="flex items-center justify-end gap-2 text-[11px] text-tx-faint pt-1">
          <span>{t('seriesDetail.synced', { time: fmt(vm.syncedAt, 'datetime') })}</span>
          {tmdbSeriesDegraded && <StaleBadge asOf={vm.syncedAt} source="tmdb" />}
          {omdbDegraded && <StaleBadge asOf={vm.syncedAt} source="omdb" />}
        </div>
      )}
    </>
  );
}
