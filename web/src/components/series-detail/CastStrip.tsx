import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ArrowRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl, type CastMember } from '@/api/series';
import { MonogramFallback } from '@/components/MonogramFallback';
import { Skeleton } from '@/components/ui/skeleton';

export interface CastStripProps {
  // Story 495 / N-1e §A3: rendered URL is composed in SeriesDetail so
  // the routing concern stays in the page that owns the URL shape.
  readonly castHref: string;
  readonly seriesId: number;
  readonly cast?: readonly CastMember[] | undefined;
  readonly limit?: number;
  readonly className?: string | undefined;
  // Story 495 / N-1e (B-20): when true AND cast is empty, render a
  // skeleton row + loading label instead of returning null.
  readonly tmdbPersonDegraded?: boolean | undefined;
}

export function CastStrip({
  castHref, seriesId, cast, limit = 8, className, tmdbPersonDegraded,
}: CastStripProps) {
  const { t } = useTranslation();
  // W19-5 (#1075): the preview shows the *main* cast — actors in the most
  // episodes — not TMDB billing order. Stable-sort a COPY by episode_count
  // desc before slicing so ties keep the incoming credit_order ASC order;
  // members with no episode_count (?? -1) sort last. The full /cast page
  // stays in billing order.
  const items = [...(cast ?? [])]
    .sort((a, b) => (b.episode_count ?? -1) - (a.episode_count ?? -1))
    .slice(0, limit);

  if (items.length === 0) {
    if (!tmdbPersonDegraded) return null;
    return (
      <section
        data-testid="cast-strip-loading"
        aria-labelledby="cast-strip-heading"
        data-series-id={seriesId}
        className={cn('flex flex-col gap-3', className)}
      >
        <div className="flex items-center justify-between gap-2.5 mb-3.5 min-w-0">
          <h2
            id="cast-strip-heading"
            className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint truncate"
          >
            {t('seriesDetail.cast.label')}
          </h2>
          <span
            data-testid="cast-strip-loading-label"
            className="shrink-0 text-[12.5px] text-tx-muted"
          >
            {t('seriesDetail.degraded.cast.loading')}
          </span>
        </div>
        <div
          className="grid gap-2.5"
          style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))' }}
        >
          {Array.from({ length: 8 }).map((_, i) => (
            <div
              key={i}
              data-testid="cast-skeleton-avatar"
              className="flex items-center gap-2.5 rounded-md min-w-0 p-[7px_9px]"
            >
              <Skeleton className="shrink-0 w-[42px] h-[42px] rounded-full" />
              <div className="flex flex-col gap-1 min-w-0 flex-1">
                <Skeleton className="h-3 w-[80%]" />
                <Skeleton className="h-2.5 w-[60%]" />
              </div>
            </div>
          ))}
        </div>
      </section>
    );
  }

  return (
    <section
      data-testid="cast-strip"
      aria-labelledby="cast-strip-heading"
      className={cn('flex flex-col gap-3', className)}
    >
      <div
        data-testid="cast-strip-header"
        className="flex items-center justify-between gap-2.5 mb-3.5 min-w-0"
      >
        <h2
          id="cast-strip-heading"
          className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint truncate"
        >
          {t('seriesDetail.cast.label')}
        </h2>
        <Link
          to={castHref}
          data-testid="cast-strip-view-all"
          className="shrink-0 inline-flex items-center gap-1 text-[12.5px] text-tx-muted hover:text-tx-primary transition-colors"
        >
          {t('seriesDetail.cast.viewAll')}
          <ArrowRight className="w-[13px] h-[13px]" aria-hidden="true" />
        </Link>
      </div>

      <div
        data-testid="cast-strip-grid"
        className="grid gap-2.5"
        style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))' }}
      >
        {items.map((m) => {
          const src = mediaUrl(m.profile_asset);
          const name = m.name ?? '';
          const character = m.character_name ?? '';
          // B-45 (audit 2026-06-25): the PersonPage route is `/person/:tmdbId`
          // and reads `tmdbId` via useParams. Sonarr-only people without a
          // TMDB match are rendered as a non-clickable card so we never
          // navigate to /person/undefined (which would silently fall through
          // the router's catch-all to the Dashboard).
          const tmdbPersonId = m.tmdb_person_id;
          const hasPerson = typeof tmdbPersonId === 'number' && tmdbPersonId > 0;
          const body = (
            <>
              <span
                className="shrink-0 w-[42px] h-[42px] rounded-full overflow-hidden border border-border-subtle bg-bg-surface-2"
                data-testid="cast-strip-avatar"
              >
                {src ? (
                  <img
                    src={src}
                    alt=""
                    aria-hidden="true"
                    loading="lazy"
                    decoding="async"
                    className="w-full h-full object-cover"
                  />
                ) : (
                  <MonogramFallback title={name} kind="avatar" />
                )}
              </span>
              <span className="flex flex-col min-w-0">
                <span
                  className="text-[12.5px] font-medium text-tx-primary truncate"
                  data-testid="cast-strip-name"
                  title={name}
                >
                  {name}
                </span>
                {character && (
                  <span
                    className="text-[11px] text-tx-muted truncate"
                    data-testid="cast-strip-character"
                    title={character}
                  >
                    {character}
                  </span>
                )}
              </span>
            </>
          );
          const className = cn(
            'flex items-center gap-2.5 rounded-md min-w-0 p-[7px_9px]',
            'border border-transparent hover:border-border-faint hover:bg-bg-surface transition-colors',
          );
          if (hasPerson) {
            return (
              <Link
                key={tmdbPersonId}
                to={`/person/${tmdbPersonId}`}
                data-testid="cast-strip-card"
                className={className}
              >
                {body}
              </Link>
            );
          }
          return (
            <div
              key={`${name}-${character}`}
              data-testid="cast-strip-card"
              data-no-link="true"
              className={className}
            >
              {body}
            </div>
          );
        })}
      </div>
    </section>
  );
}
