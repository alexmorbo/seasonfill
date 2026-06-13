import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { ExternalLink, Play, BookmarkCheck, Ellipsis } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { useInstancePublicURL } from '@/lib/useInstancePublicURL';
import { slugifyTitle, buildSonarrSeriesHref } from '@/lib/sonarrUrl';
import { mediaUrl, parseStatus, isSonarrOnly, type SeriesHero as HeroDTO } from '@/api/seriesDetail';
import { StatusPill } from './StatusPill';
import { RatingDuo } from './RatingDuo';
import { StaleBadge } from './StaleBadge';

export interface SeriesHeroProps {
  readonly instance: string;
  readonly seriesId: number;
  readonly hero: HeroDTO | undefined;
  readonly tmdbStaleAt?: string | undefined;
  readonly imdbStaleAt?: string | undefined;
  readonly titleSlug?: string | undefined;
}

function yearRange(start: number | undefined, end: number | undefined, status: string): string {
  if (!start) return '';
  // Continuing series: "2019–" (em dash, no end). Otherwise "2019–2024" / "2019".
  if (status === 'continuing' || status === 'in_production') return `${start}–`;
  if (!end || end === start) return String(start);
  return `${start}–${end}`;
}

export function SeriesHero({
  instance, hero, tmdbStaleAt, imdbStaleAt, titleSlug,
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
  const networks = (hero?.networks ?? []).slice(0, 3);
  const contentRating = hero?.content_rating;
  const backdropSrc = mediaUrl(hero?.backdrop_asset);
  const posterSrc = mediaUrl(hero?.poster_asset);
  const trailerKey = hero?.trailer?.key;
  const showTrailer = Boolean(trailerKey) && !sonarrOnly;
  const sonarrHref = sonarrPublic
    ? buildSonarrSeriesHref(sonarrPublic, titleSlug && titleSlug.length > 0 ? titleSlug : slugifyTitle(title))
    : undefined;
  const monogram = (title.charAt(0) || '?').toUpperCase();
  const showRatings = !sonarrOnly && (hero?.tmdb_rating || hero?.imdb_rating);

  return (
    <section
      data-testid="series-hero"
      data-sonarr-only={sonarrOnly ? 'true' : 'false'}
      className={cn(
        'relative isolate overflow-hidden rounded-xl border border-border-faint',
        sonarrOnly ? 'bg-bg-surface' : 'bg-bg-base',
      )}
      style={{ minHeight: 'clamp(360px, 38vh, 460px)' }}
    >
      {/* Backdrop layer (hidden in Sonarr-only) */}
      {!sonarrOnly && backdropSrc && (
        <img
          src={backdropSrc}
          alt=""
          aria-hidden="true"
          loading="eager"
          decoding="async"
          className="absolute inset-0 w-full h-full object-cover object-top z-0"
          data-testid="hero-backdrop"
        />
      )}
      {!sonarrOnly && (
        <div
          aria-hidden="true"
          className="absolute inset-0 z-[1]"
          style={{
            background:
              'linear-gradient(105deg, oklch(0.13 0.01 270 / 0.94) 0%, oklch(0.14 0.01 270 / 0.72) 38%, oklch(0.16 0.01 270 / 0.15) 100%),' +
              'linear-gradient(0deg, oklch(0.13 0.01 270 / 0.96) 2%, transparent 46%)',
          }}
        />
      )}

      {/* Content layer */}
      <div className="relative z-[2] flex flex-col gap-5 p-6 md:p-8 lg:flex-row lg:items-end">
        {/* Poster — inline column on lg+, stacked above title on smaller */}
        <div
          className={cn(
            'shrink-0 self-start rounded-lg overflow-hidden border border-border-subtle bg-bg-surface-2 shadow-lg',
            'w-[140px] h-[210px] md:w-[168px] md:h-[252px] min-[1440px]:w-[200px] min-[1440px]:h-[300px]',
          )}
          data-testid="hero-poster"
        >
          {posterSrc ? (
            <img src={posterSrc} alt="" aria-hidden="true" className="w-full h-full object-cover" />
          ) : (
            <span className="flex items-center justify-center w-full h-full font-mono font-bold text-[64px] text-tx-faint">
              {monogram}
            </span>
          )}
        </div>

        {/* Meta column */}
        <div className="flex flex-col gap-3 min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-3">
            <h1
              data-testid="hero-title"
              className="text-[26px] md:text-[32px] font-bold tracking-tight text-tx-primary leading-tight"
            >
              {title}
            </h1>
            <StatusPill status={status} />
            {tmdbStaleAt && !sonarrOnly && <StaleBadge asOf={tmdbStaleAt} source="tmdb" />}
          </div>
          {originalTitle && (
            <div className="text-[13px] text-tx-muted -mt-1">{originalTitle}</div>
          )}
          {tagline && (
            <p className="italic text-[14px] text-tx-secondary -mt-1">{tagline}</p>
          )}

          <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 text-[12.5px] text-tx-secondary">
            <span className="mono tabular-nums">{yearRange(hero?.year_start, hero?.year_end, status)}</span>
            {hero?.runtime_minutes && hero.runtime_minutes > 0 && (
              <>
                <span aria-hidden="true" className="chip-dot bg-tx-faint" />
                <span>{t('seriesDetail.hero.runtime', { mins: hero.runtime_minutes })}</span>
              </>
            )}
            {contentRating?.rating && (
              <>
                <span aria-hidden="true" className="chip-dot bg-tx-faint" />
                <span className="rounded border border-border-subtle px-1.5 py-0.5 text-[10.5px] font-semibold">
                  {contentRating.rating}
                </span>
              </>
            )}
            {genres.length > 0 && (
              <>
                <span aria-hidden="true" className="chip-dot bg-tx-faint" />
                <span className="inline-flex flex-wrap gap-1.5">
                  {genres.map((g) => (
                    <span
                      key={g.id ?? g.name}
                      className="rounded-md bg-bg-surface-2/70 border border-border-subtle px-1.5 py-0.5 text-[11px]"
                    >
                      {g.name}
                    </span>
                  ))}
                </span>
              </>
            )}
            {networks.length > 0 && (
              <span className="inline-flex items-center gap-2 ml-1">
                {networks.map((n) => (
                  <span
                    key={n.id ?? n.name}
                    title={n.name ?? ''}
                    className="text-[10.5px] font-bold tracking-widest uppercase text-tx-muted"
                  >
                    {n.name}
                  </span>
                ))}
              </span>
            )}
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
            {showTrailer && (
              <Button asChild size="sm" data-testid="hero-action-trailer">
                <a
                  href={`https://www.youtube.com/watch?v=${trailerKey}`}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <Play className="w-3.5 h-3.5" aria-hidden="true" />
                  {t('seriesDetail.hero.trailer')}
                </a>
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
      </div>
    </section>
  );
}
