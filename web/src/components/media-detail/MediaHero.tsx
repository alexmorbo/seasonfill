import { useState, Fragment } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ExternalLink, Play, BookmarkCheck, Ellipsis, ChevronLeft, ChevronDown, Plus } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { mediaUrl } from '@/api/series';
import { RatingDuo } from '@/components/series-detail/RatingDuo';
import { StaleBadge } from '@/components/series-detail/StaleBadge';
import { TrailerModal } from '@/components/series-detail/TrailerModal';
import { MonogramFallback } from '@/components/MonogramFallback';
import type { MediaDetailVM } from './view-model';

// U-4 sub-step B — `MediaHero` only ever reads this subset of
// `MediaDetailVM`. Keeping the prop narrowed to a `Pick` (rather than the
// full VM) means the series/movie hero ADAPTER (`SeriesHero.tsx`) can build
// just this slice instead of a full `MediaDetailVM` padded with unused
// placeholder fields (cast/recommendations/sidebarFacts/…) — a full VM
// still satisfies this type structurally, so `MediaDetail.tsx` passing its
// whole `vm` through is unaffected.
export type MediaHeroVM = Pick<
  MediaDetailVM,
  | 'type'
  | 'sonarrOnly'
  | 'localizedTitle'
  | 'originalTitle'
  | 'tagline'
  | 'yearLabel'
  | 'runtimeMinutes'
  | 'contentRating'
  | 'genres'
  | 'posterAsset'
  | 'backdropAsset'
  | 'backdropLoadingLabel'
  | 'ratings'
  | 'actions'
  | 'trailer'
  | 'heroActions'
>;

export interface MediaHeroProps {
  readonly vm: MediaHeroVM;
  readonly heroExtras?: {
    readonly nextCard?: ReactNode;
    readonly bottomStrip?: ReactNode;
  } | undefined;
}

export function MediaHero({ vm, heroExtras }: MediaHeroProps) {
  const { t } = useTranslation();
  const [trailerOpen, setTrailerOpen] = useState(false);

  const { sonarrOnly, heroActions } = vm;
  const title = vm.localizedTitle;
  const backdropSrc = mediaUrl(vm.backdropAsset);
  const posterSrc = mediaUrl(vm.posterAsset);
  const trailerKey = vm.trailer?.key;
  const trailerSite = vm.trailer?.site;
  const showTrailer = Boolean(trailerKey)
    && !sonarrOnly
    && (!trailerSite || trailerSite.toLowerCase() === 'youtube');
  const showRatings = !sonarrOnly && (vm.ratings.tmdb || vm.ratings.imdb || vm.ratings.imdbLoading);
  const fallback = sonarrOnly ? 'sonarr-only' : 'none';

  return (
    <section
      data-testid={vm.type === 'series' ? 'series-hero' : 'movie-hero'}
      data-sonarr-only={sonarrOnly ? 'true' : 'false'}
      data-fallback={fallback}
      className="sd-hero-bleed"
    >
      {/* In-hero back-link — glass chip at top-left, above scrim/inner. */}
      <Link
        to={heroActions.backHref}
        className="sd-back-link"
        data-testid="hero-back-link"
      >
        <span data-testid="series-detail-back" className="inline-flex items-center gap-1">
          <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" />
          {heroActions.backLabel}
        </span>
      </Link>

      {/* Backdrop layer — full-bleed, masked. */}
      <div className="sd-backdrop-layer" aria-hidden="true" data-testid="hero-backdrop-layer">
        {!sonarrOnly && backdropSrc && (
          <img
            src={backdropSrc}
            alt=""
            loading="eager"
            decoding="async"
            data-testid="hero-backdrop"
          />
        )}
        {!sonarrOnly && !backdropSrc && (
          <MonogramFallback
            title={title}
            kind="backdrop"
            {...(vm.backdropLoadingLabel ? { loadingLabel: vm.backdropLoadingLabel } : {})}
          />
        )}
      </div>

      {/* Scrim — gradient over backdrop for text legibility. */}
      {!sonarrOnly && (
        <div className="sd-scrim-layer" aria-hidden="true" data-testid="hero-scrim" />
      )}

      {/* Inner content. */}
      <div className="sd-hero-inner">
        {/* Poster (left column, full-height, bottom-aligned). */}
        <div
          className="sd-poster border border-border-subtle bg-bg-surface-2 shadow-lg"
          data-testid="hero-poster"
        >
          {posterSrc ? (
            <img src={posterSrc} alt="" aria-hidden="true" className="w-full h-full object-cover" />
          ) : (
            <MonogramFallback title={title} kind="poster" />
          )}
        </div>

        {/* Right column (column-flex, two stacked rows). */}
        <div className="sd-hero-right">
          {/* Top row — meta + next-card (over divider). */}
          <div className="sd-hero-cols">
            <div className="sd-hmeta flex flex-col gap-3 text-white">
              <div className="flex flex-wrap items-center gap-3">
                <h1
                  data-testid="hero-title"
                  className="text-[26px] md:text-[32px] font-bold tracking-tight text-white leading-tight"
                >
                  {title}
                </h1>
                {vm.ratings.tmdbStaleAt && !sonarrOnly && (
                  <StaleBadge asOf={vm.ratings.tmdbStaleAt} source="tmdb" />
                )}
              </div>
              {vm.originalTitle && (
                <div className="text-[13px] text-white/65 -mt-1">{vm.originalTitle}</div>
              )}
              {vm.tagline && (
                <p className="italic text-[14px] text-white/80 -mt-1">{vm.tagline}</p>
              )}

              <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 text-[12.5px] text-white/85">
                <span className="font-mono tabular-nums">{vm.yearLabel}</span>
                {vm.runtimeMinutes && vm.runtimeMinutes > 0 && (
                  <>
                    <span aria-hidden="true" className="w-1 h-1 rounded-full bg-white/40" />
                    <span>{t('seriesDetail.hero.runtime', { mins: vm.runtimeMinutes })}</span>
                  </>
                )}
                {vm.contentRating?.rating && (
                  <>
                    <span aria-hidden="true" className="w-1 h-1 rounded-full bg-white/40" />
                    <span className="rounded border border-white/30 px-1.5 py-0.5 text-[10.5px] font-semibold">
                      {vm.contentRating.rating}
                    </span>
                  </>
                )}
                {vm.genres.length > 0 && (
                  <>
                    <span aria-hidden="true" className="w-1 h-1 rounded-full bg-white/40" />
                    <span className="inline-flex flex-wrap gap-1.5">
                      {vm.genres.map((g) => (
                        <span
                          key={g.id ?? g.name}
                          className="rounded-md bg-white/[0.10] border border-white/15 px-1.5 py-0.5 text-[11px]"
                        >
                          {g.name}
                        </span>
                      ))}
                    </span>
                  </>
                )}
                {/* Networks intentionally REMOVED from hero in v2 — moved to rail-card. */}
              </div>

              {showRatings && (
                <RatingDuo
                  {...(vm.ratings.tmdb ? { tmdb: vm.ratings.tmdb } : {})}
                  {...(vm.ratings.imdb ? { imdb: vm.ratings.imdb } : {})}
                  {...(vm.ratings.imdbHref ? { imdbHref: vm.ratings.imdbHref } : {})}
                  {...(vm.ratings.imdbStaleAt ? { imdbStaleAt: vm.ratings.imdbStaleAt } : {})}
                  {...(vm.ratings.imdbLoading ? { imdbLoading: true } : {})}
                />
              )}

              <div className="flex flex-wrap items-center gap-2 pt-1">
                {(heroActions.sonarrHref || heroActions.showAddToSonarr || heroActions.showCaret) && (
                  <div className="inline-flex items-center" data-testid="hero-action-split">
                    {heroActions.sonarrHref && (
                      <Button
                        asChild
                        variant="outline"
                        size="sm"
                        className={cn(heroActions.showCaret && 'rounded-r-none')}
                        data-testid="hero-action-sonarr"
                      >
                        <a href={heroActions.sonarrHref} target="_blank" rel="noopener noreferrer">
                          <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
                          {t('common.openInSonarr')}
                        </a>
                      </Button>
                    )}
                    {heroActions.showAddToSonarr && !heroActions.sonarrHref && (
                      <Button
                        variant="outline"
                        size="sm"
                        className={cn(heroActions.showCaret && 'rounded-r-none')}
                        data-testid="hero-action-add-to-sonarr"
                        onClick={heroActions.onAddToSonarr}
                      >
                        <Plus className="w-3.5 h-3.5" aria-hidden="true" />
                        {t('discovery.add.button')}
                      </Button>
                    )}
                    {heroActions.showCaret && (
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="outline"
                            size="sm"
                            className="-ml-px rounded-l-none px-1.5"
                            aria-label={t('common.actions')}
                            data-testid="hero-action-caret"
                          >
                            <ChevronDown className="w-3.5 h-3.5" aria-hidden="true" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {heroActions.openItems.map(({ name, href }) =>
                            href ? (
                              <DropdownMenuItem key={`open-${name}`} asChild data-testid={`hero-menu-open-${name}`}>
                                <a href={href} target="_blank" rel="noopener noreferrer">
                                  <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
                                  {t('seriesDetail.hero.openInInstance', { name })}
                                </a>
                              </DropdownMenuItem>
                            ) : (
                              <DropdownMenuItem key={`open-${name}`} disabled data-testid={`hero-menu-open-${name}`}>
                                <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
                                {t('seriesDetail.hero.openInInstance', { name })}
                              </DropdownMenuItem>
                            ),
                          )}
                          {heroActions.openItems.length > 0
                            && heroActions.showAddToSonarr
                            && heroActions.addItems.length > 0 && (
                            <DropdownMenuSeparator />
                          )}
                          {heroActions.showAddToSonarr
                            && heroActions.addItems.map((name) => (
                              <DropdownMenuItem
                                key={`add-${name}`}
                                data-testid={`hero-menu-add-${name}`}
                                onSelect={() => heroActions.onAddToInstance(name)}
                              >
                                <Plus className="w-3.5 h-3.5" aria-hidden="true" />
                                {t('discovery.add.addToInstance', { name })}
                              </DropdownMenuItem>
                            ))}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </div>
                )}
                {vm.type === 'movie' && vm.actions.map((a) => (
                  <Fragment key={a.id}>{a.node}</Fragment>
                ))}
                {showTrailer && trailerKey && (
                  <Button
                    size="sm"
                    data-testid="hero-action-trailer"
                    onClick={() => setTrailerOpen(true)}
                  >
                    <Play className="w-3.5 h-3.5" aria-hidden="true" />
                    {t('seriesDetail.hero.trailer')}
                  </Button>
                )}
                {heroActions.followButton}
                {vm.type === 'series' && (
                  <>
                    <Button variant="outline" size="sm" data-testid="hero-action-monitored" disabled>
                      <BookmarkCheck className="w-3.5 h-3.5" aria-hidden="true" />
                      {t('seriesDetail.hero.monitored')}
                    </Button>
                    <Button variant="ghost" size="icon" aria-label={t('common.actions')} disabled>
                      <Ellipsis className="w-4 h-4" aria-hidden="true" />
                    </Button>
                  </>
                )}
              </div>
            </div>

            {/* Glass next-card — anchored right of the meta row, above the divider. */}
            {!sonarrOnly && heroExtras?.nextCard && (
              <div className="sd-next-wrap" data-testid="hero-next-wrap">
                {heroExtras.nextCard}
              </div>
            )}
          </div>

          {/* Bottom row — on-disk strip + divider. */}
          {heroExtras?.bottomStrip}
        </div>
      </div>

      {showTrailer && trailerKey && (
        <TrailerModal
          open={trailerOpen}
          onOpenChange={setTrailerOpen}
          youtubeKey={trailerKey}
          {...(vm.trailer?.name ? { name: vm.trailer.name } : {})}
        />
      )}
    </section>
  );
}
