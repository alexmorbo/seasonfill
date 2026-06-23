import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ExternalLink, Play, BookmarkCheck, Ellipsis, ChevronLeft } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { useInstancePublicURL } from '@/lib/useInstancePublicURL';
import { slugifyTitle, buildSonarrSeriesHref } from '@/lib/sonarrUrl';
import {
  mediaUrl, parseStatus, isSonarrOnly,
  type SeriesHero as HeroDTO,
  type LibraryStrip,
  type DownloadChip,
  type NextEpisode,
} from '@/api/series';
import { RatingDuo } from './RatingDuo';
import { StaleBadge } from './StaleBadge';
import { TrailerModal } from './TrailerModal';
import { MonogramFallback } from '@/components/MonogramFallback';
import { NextEpisodeCard } from './NextEpisodeCard';
import { HeroLibraryStrip } from './HeroLibraryStrip';

export interface SeriesHeroProps {
  readonly instance: string;
  readonly seriesId: number;
  readonly hero: HeroDTO | undefined;
  readonly library?: LibraryStrip | undefined;
  readonly download?: DownloadChip | undefined;
  readonly tmdbStaleAt?: string | undefined;
  readonly imdbStaleAt?: string | undefined;
  readonly titleSlug?: string | undefined;
  readonly onScrollToTorrents?: () => void;
}

function yearRange(start: number | undefined, end: number | undefined, status: string): string {
  if (!start) return '';
  if (status === 'continuing' || status === 'in_production') return `${start}–`;
  if (!end || end === start) return String(start);
  return `${start}–${end}`;
}

export function SeriesHero({
  instance, hero, library, download, tmdbStaleAt, imdbStaleAt, titleSlug,
  onScrollToTorrents,
}: SeriesHeroProps) {
  const { t } = useTranslation();
  const sonarrPublic = useInstancePublicURL(instance);
  const status = parseStatus(hero?.status);
  const sonarrOnly = useMemo(() => isSonarrOnly(hero), [hero]);
  const title = hero?.title ?? '';
  const originalTitle = hero?.original_title && hero.original_title !== title
    ? hero.original_title : undefined;
  const tagline = sonarrOnly ? undefined : hero?.tagline;
  const genres = sonarrOnly ? [] : (hero?.genres ?? []).slice(0, 5);
  const contentRating = hero?.content_rating;
  const backdropSrc = mediaUrl(hero?.backdrop_asset);
  const posterSrc = mediaUrl(hero?.poster_asset);
  const trailer = hero?.trailer;
  const trailerKey = trailer?.key;
  const trailerSite = trailer?.site;
  const showTrailer = Boolean(trailerKey)
    && !sonarrOnly
    && (!trailerSite || trailerSite.toLowerCase() === 'youtube');
  const sonarrHref = sonarrPublic
    ? buildSonarrSeriesHref(sonarrPublic, titleSlug && titleSlug.length > 0 ? titleSlug : slugifyTitle(title))
    : undefined;
  const showRatings = !sonarrOnly && (hero?.tmdb_rating || hero?.imdb_rating);
  const nextEpisode: NextEpisode | undefined = hero?.next_episode;

  const [trailerOpen, setTrailerOpen] = useState(false);

  const fallback = sonarrOnly ? 'sonarr-only' : 'none';

  return (
    <section
      data-testid="series-hero"
      data-sonarr-only={sonarrOnly ? 'true' : 'false'}
      data-fallback={fallback}
      className={cn('sd-hero-bleed')}
    >
      {/* In-hero back-link — glass chip at top-left, above scrim/inner. */}
      <Link
        to="/series"
        className="sd-back-link"
        data-testid="hero-back-link"
      >
        <span data-testid="series-detail-back" className="inline-flex items-center gap-1">
          <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" />
          {t('seriesDetail.back')}
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
          <MonogramFallback title={title} kind="backdrop" />
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
                {tmdbStaleAt && !sonarrOnly && <StaleBadge asOf={tmdbStaleAt} source="tmdb" />}
              </div>
              {originalTitle && (
                <div className="text-[13px] text-white/65 -mt-1">{originalTitle}</div>
              )}
              {tagline && (
                <p className="italic text-[14px] text-white/80 -mt-1">{tagline}</p>
              )}

              <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 text-[12.5px] text-white/85">
                <span className="font-mono tabular-nums">{yearRange(hero?.year_start, hero?.year_end, status)}</span>
                {hero?.runtime_minutes && hero.runtime_minutes > 0 && (
                  <>
                    <span aria-hidden="true" className="w-1 h-1 rounded-full bg-white/40" />
                    <span>{t('seriesDetail.hero.runtime', { mins: hero.runtime_minutes })}</span>
                  </>
                )}
                {contentRating?.rating && (
                  <>
                    <span aria-hidden="true" className="w-1 h-1 rounded-full bg-white/40" />
                    <span className="rounded border border-white/30 px-1.5 py-0.5 text-[10.5px] font-semibold">
                      {contentRating.rating}
                    </span>
                  </>
                )}
                {genres.length > 0 && (
                  <>
                    <span aria-hidden="true" className="w-1 h-1 rounded-full bg-white/40" />
                    <span className="inline-flex flex-wrap gap-1.5">
                      {genres.map((g) => (
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
                  {...(hero?.tmdb_rating ? { tmdb: hero.tmdb_rating } : {})}
                  {...(hero?.imdb_rating ? { imdb: hero.imdb_rating } : {})}
                  {...(imdbStaleAt ? { imdbStaleAt } : {})}
                />
              )}

              <div className="flex flex-wrap items-center gap-2 pt-1">
                {sonarrHref && (
                  <Button asChild variant="outline" size="sm" data-testid="hero-action-sonarr">
                    <a href={sonarrHref} target="_blank" rel="noopener noreferrer">
                      <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
                      {t('common.openInSonarr')}
                    </a>
                  </Button>
                )}
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
                <Button variant="outline" size="sm" data-testid="hero-action-monitored" disabled>
                  <BookmarkCheck className="w-3.5 h-3.5" aria-hidden="true" />
                  {t('seriesDetail.hero.monitored')}
                </Button>
                <Button variant="ghost" size="icon" aria-label={t('common.actions')} disabled>
                  <Ellipsis className="w-4 h-4" aria-hidden="true" />
                </Button>
              </div>
            </div>

            {/* Glass next-card — anchored right of the meta row, above the divider. */}
            {!sonarrOnly && (
              <div className="sd-next-wrap" data-testid="hero-next-wrap">
                <NextEpisodeCard
                  variant="glass"
                  status={status}
                  {...(nextEpisode ? { nextEpisode } : {})}
                  {...(hero?.year_end ? { yearEnd: hero.year_end } : {})}
                />
              </div>
            )}
          </div>

          {/* Bottom row — on-disk strip + divider. */}
          <HeroLibraryStrip
            tone={sonarrOnly ? 'light' : 'dark'}
            {...(library ? { library } : {})}
            {...(download ? { download } : {})}
            {...(onScrollToTorrents ? { onDownloadClick: onScrollToTorrents } : {})}
          />
        </div>
      </div>

      {showTrailer && trailerKey && (
        <TrailerModal
          open={trailerOpen}
          onOpenChange={setTrailerOpen}
          youtubeKey={trailerKey}
          {...(trailer?.name ? { name: trailer.name } : {})}
        />
      )}
    </section>
  );
}
