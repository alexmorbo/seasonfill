import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ExternalLink, Play, BookmarkCheck, Ellipsis, ChevronLeft, ChevronDown, Plus } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { useInstances } from '@/lib/instances';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { slugifyTitle, buildSonarrSeriesHref } from '@/lib/sonarrUrl';
import {
  mediaUrl, parseStatus, isSonarrOnly,
  type SeriesHero as HeroDTO,
  type RatingScore,
  type LibraryStrip,
  type DownloadChip,
  type NextEpisode,
} from '@/api/series';
import { useSeriesRatings } from '@/api/seriesRatings';
import { RatingDuo } from './RatingDuo';
import { StaleBadge } from './StaleBadge';
import { TrailerModal } from './TrailerModal';
import { MonogramFallback } from '@/components/MonogramFallback';
import { NextEpisodeCard } from './NextEpisodeCard';
import { HeroLibraryStrip } from './HeroLibraryStrip';
import { FollowButton } from '@/components/follow/FollowButton';
import {
  useAddToSonarrLauncher,
  type AddToSonarrTarget,
} from '@/components/discovery/add-to-sonarr-context';

export interface SeriesHeroProps {
  // Story 495 / N-1e: now optional — TMDB-only series carry no
  // primary instance; SeriesDetail picks `in_library_instances[0]`
  // and passes undefined when empty. An undefined `instance` yields no
  // `sonarrHref` because `publicUrlByName.get(instance)` is undefined
  // (the `instance ? publicUrlByName.get(instance) : undefined` ternary).
  readonly instance: string | undefined;
  readonly seriesId: number;
  readonly hero: HeroDTO | undefined;
  readonly library?: LibraryStrip | undefined;
  readonly download?: DownloadChip | undefined;
  readonly tmdbStaleAt?: string | undefined;
  readonly imdbStaleAt?: string | undefined;
  readonly titleSlug?: string | undefined;
  readonly onScrollToTorrents?: () => void;
  // Story 495 / N-1e (B-20): when true AND no backdrop_asset, render
  // the MonogramFallback with a "loading backdrop" plate overlay.
  readonly tmdbSeriesDegraded?: boolean | undefined;
  // Story 495 / N-1e (B-20): when true AND no imdb_rating, render a
  // skeleton chip on the IMDb side of `<RatingDuo>`.
  readonly imdbLoading?: boolean | undefined;
  // TMDB-only (not-in-library) series carry an add target; SeriesDetail
  // builds it only when `in_library_instances[0]` is undefined. Presence here
  // ⇒ render the hero "Add to Sonarr" button (mutually exclusive with the
  // "Open in Sonarr" button, which needs a resolved instance href).
  readonly addToSonarrTarget?: AddToSonarrTarget | undefined;
  readonly inLibraryInstances?: readonly string[];
}

function yearRange(start: number | undefined, end: number | undefined, status: string): string {
  if (!start) return '';
  if (status === 'continuing' || status === 'in_production') return `${start}–`;
  if (!end || end === start) return String(start);
  return `${start}–${end}`;
}

export function SeriesHero({
  instance, seriesId, hero, library, download, tmdbStaleAt, imdbStaleAt, titleSlug,
  onScrollToTorrents, tmdbSeriesDegraded, imdbLoading, addToSonarrTarget,
  inLibraryInstances = [],
}: SeriesHeroProps) {
  const { t } = useTranslation();
  const { openAddToSonarr } = useAddToSonarrLauncher();
  const instancesQ = useInstances();
  const publicUrlByName = useMemo(() => {
    const m = new Map<string, string>();
    for (const i of instancesQ.data?.instances ?? []) {
      if (i.name && i.public_url) m.set(i.name, i.public_url);
    }
    return m;
  }, [instancesQ.data?.instances]);
  const sonarrPublic = instance ? publicUrlByName.get(instance) : undefined;
  // #1059 / F-11-FE — shared live /ratings query (same key as RatingsSection ⇒
  // react-query dedups to one fetch). Drives the effective hero ★ values below.
  const { data: liveRatings } = useSeriesRatings({ seriesId });
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
  const slug = titleSlug && titleSlug.length > 0 ? titleSlug : slugifyTitle(title);
  const sonarrHref = sonarrPublic
    ? buildSonarrSeriesHref(sonarrPublic, slug)
    : undefined;

  const allInstances = instancesQ.data?.instances ?? [];
  const inLibrarySet = new Set(inLibraryInstances);
  const openItems = inLibraryInstances
    .filter((name) => Boolean(name))
    .map((name) => {
      const url = publicUrlByName.get(name);
      return { name, href: url ? buildSonarrSeriesHref(url, slug) : undefined };
    });
  const addItems = allInstances
    .map((i) => i.name)
    .filter((name): name is string =>
      typeof name === 'string' && name.length > 0 && !inLibrarySet.has(name));
  const showCaret = allInstances.length > 1;
  // #1059 / F-11-FE — single-source the hero ★ off the live /ratings query
  // RatingsSection also consumes (react-query dedups the shared key ⇒ one
  // fetch, identical numbers, no post-refresh divergence). The skeleton hero
  // rating is the instant first-paint fallback until /ratings resolves; the
  // imdbLoading / StaleBadge / degraded prop-wiring is UNCHANGED (those are
  // orthogonal freshness signals, not rating values).
  const tmdbScore: RatingScore | undefined =
    typeof liveRatings?.tmdb_rating === 'number' && liveRatings.tmdb_rating > 0
      ? {
          score: liveRatings.tmdb_rating,
          ...(liveRatings.tmdb_votes && liveRatings.tmdb_votes > 0
            ? { votes: liveRatings.tmdb_votes }
            : {}),
        }
      : hero?.tmdb_rating;
  const imdbScore: RatingScore | undefined =
    typeof liveRatings?.imdb_rating === 'number' && liveRatings.imdb_rating > 0
      ? {
          score: liveRatings.imdb_rating,
          ...(liveRatings.imdb_votes && liveRatings.imdb_votes > 0
            ? { votes: liveRatings.imdb_votes }
            : {}),
        }
      : hero?.imdb_rating;
  const showRatings = !sonarrOnly && (tmdbScore || imdbScore || imdbLoading);
  const nextEpisode: NextEpisode | undefined = hero?.next_episode;
  const backdropLoadingLabel = !sonarrOnly && !backdropSrc && tmdbSeriesDegraded
    ? t('seriesDetail.degraded.backdrop.loading')
    : undefined;

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
          <MonogramFallback
            title={title}
            kind="backdrop"
            {...(backdropLoadingLabel ? { loadingLabel: backdropLoadingLabel } : {})}
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
                  {...(tmdbScore ? { tmdb: tmdbScore } : {})}
                  {...(imdbScore ? { imdb: imdbScore } : {})}
                  {...(imdbStaleAt ? { imdbStaleAt } : {})}
                  {...(imdbLoading ? { imdbLoading: true } : {})}
                />
              )}

              <div className="flex flex-wrap items-center gap-2 pt-1">
                {(sonarrHref || addToSonarrTarget || showCaret) && (
                  <div className="inline-flex items-center" data-testid="hero-action-split">
                    {sonarrHref && (
                      <Button
                        asChild
                        variant="outline"
                        size="sm"
                        className={cn(showCaret && 'rounded-r-none')}
                        data-testid="hero-action-sonarr"
                      >
                        <a href={sonarrHref} target="_blank" rel="noopener noreferrer">
                          <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
                          {t('common.openInSonarr')}
                        </a>
                      </Button>
                    )}
                    {addToSonarrTarget && !sonarrHref && (
                      <Button
                        variant="outline"
                        size="sm"
                        className={cn(showCaret && 'rounded-r-none')}
                        data-testid="hero-action-add-to-sonarr"
                        onClick={() => openAddToSonarr(addToSonarrTarget)}
                      >
                        <Plus className="w-3.5 h-3.5" aria-hidden="true" />
                        {t('discovery.add.button')}
                      </Button>
                    )}
                    {showCaret && (
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
                          {openItems.map(({ name, href }) =>
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
                          {openItems.length > 0 && addToSonarrTarget && addItems.length > 0 && (
                            <DropdownMenuSeparator />
                          )}
                          {addToSonarrTarget &&
                            addItems.map((name) => (
                              <DropdownMenuItem
                                key={`add-${name}`}
                                data-testid={`hero-menu-add-${name}`}
                                onSelect={() => openAddToSonarr({ ...addToSonarrTarget, instanceName: name })}
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
                <FollowButton seriesId={seriesId} />
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
