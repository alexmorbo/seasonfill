import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ArrowRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/series';
import type { MovieCastMember } from '@/api/movieCast';
import { MonogramFallback } from '@/components/MonogramFallback';
import { LanguageFallbackTag } from '@/components/series-detail/LanguageFallbackTag';
import { Skeleton } from '@/components/ui/skeleton';

export interface MovieCastStripProps {
  readonly tmdbId: number;
  readonly cast?: readonly MovieCastMember[] | undefined;
  // served_language from dto.MovieCastResponse. Feeds LanguageFallbackTag
  // (contentLang) to signal a localized-title fallback — mirroring how
  // SeriesDetail surfaces served_language. The movie /cast `degraded`
  // marker is "missing_lang", which is NOT a member of series'
  // DegradedSource union, so DegradedChip does not apply here.
  readonly servedLanguage?: string | undefined;
  readonly requestedLang?: string | undefined;
  // Optional "view all" target. Movies have no dedicated cast page yet, so
  // the link is omitted when this is absent.
  readonly castHref?: string | undefined;
  readonly limit?: number;
  readonly className?: string | undefined;
  // When true AND cast is empty, render a skeleton row + loading label
  // instead of returning null (mirrors CastStrip's tmdbPersonDegraded).
  readonly loading?: boolean | undefined;
}

export function MovieCastStrip({
  tmdbId,
  cast,
  servedLanguage,
  requestedLang,
  castHref,
  limit = 8,
  className,
  loading,
}: MovieCastStripProps) {
  const { t } = useTranslation();
  // BE returns credit_order ASC NULLS LAST — DO NOT re-sort. Just cap to the
  // preview limit in received order.
  const items = (cast ?? []).slice(0, limit);

  if (items.length === 0) {
    if (!loading) return null;
    return (
      <section
        data-testid="movie-cast-strip-loading"
        aria-labelledby="movie-cast-strip-heading"
        data-tmdb-id={tmdbId}
        className={cn('flex flex-col gap-3', className)}
      >
        <div className="flex items-center justify-between gap-2.5 mb-3.5 min-w-0">
          <h2
            id="movie-cast-strip-heading"
            className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint truncate"
          >
            {t('movieDetail.cast.label')}
          </h2>
          <span
            data-testid="movie-cast-strip-loading-label"
            className="shrink-0 text-[12.5px] text-tx-muted"
          >
            {t('movieDetail.cast.loading')}
          </span>
        </div>
        <div
          className="grid gap-2.5"
          style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))' }}
        >
          {Array.from({ length: 8 }).map((_, i) => (
            <div
              key={i}
              data-testid="movie-cast-skeleton-avatar"
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
      data-testid="movie-cast-strip"
      aria-labelledby="movie-cast-strip-heading"
      className={cn('flex flex-col gap-3', className)}
    >
      <div
        data-testid="movie-cast-strip-header"
        className="flex items-center justify-between gap-2.5 mb-3.5 min-w-0"
      >
        <h2
          id="movie-cast-strip-heading"
          className="inline-flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint truncate"
        >
          {t('movieDetail.cast.label')}
          <LanguageFallbackTag
            contentLang={servedLanguage}
            {...(requestedLang ? { requestedLang } : {})}
            testid="movie-cast-lang-fallback"
          />
        </h2>
        {castHref && (
          <Link
            to={castHref}
            data-testid="movie-cast-strip-view-all"
            className="shrink-0 inline-flex items-center gap-1 text-[12.5px] text-tx-muted hover:text-tx-primary transition-colors"
          >
            {t('movieDetail.cast.viewAll')}
            <ArrowRight className="w-[13px] h-[13px]" aria-hidden="true" />
          </Link>
        )}
      </div>

      <div
        data-testid="movie-cast-strip-grid"
        className="grid gap-2.5"
        style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))' }}
      >
        {items.map((m, idx) => {
          const src = mediaUrl(m.profile_asset);
          const name = m.name ?? '';
          const character = m.character_name ?? '';
          // Person link target is the TMDB person id (dto field `tmdb_id`),
          // mirroring the series strip's tmdb_person_id. Route is
          // `/person/:tmdbId`. People with no TMDB match render a
          // non-clickable card so we never navigate to /person/undefined.
          const personTmdbId = m.tmdb_id;
          const hasPerson = typeof personTmdbId === 'number' && personTmdbId > 0;
          const body = (
            <>
              <span
                className="shrink-0 w-[42px] h-[42px] rounded-full overflow-hidden border border-border-subtle bg-bg-surface-2"
                data-testid="movie-cast-strip-avatar"
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
                  data-testid="movie-cast-strip-name"
                  title={name}
                >
                  {name}
                </span>
                {character && (
                  <span
                    className="text-[11px] text-tx-muted truncate"
                    data-testid="movie-cast-strip-character"
                    title={character}
                  >
                    {character}
                  </span>
                )}
              </span>
            </>
          );
          const cardClass = cn(
            'flex items-center gap-2.5 rounded-md min-w-0 p-[7px_9px]',
            'border border-transparent hover:border-border-faint hover:bg-bg-surface transition-colors',
          );
          if (hasPerson) {
            return (
              <Link
                key={personTmdbId}
                to={`/person/${personTmdbId}`}
                data-testid="movie-cast-strip-card"
                className={cardClass}
              >
                {body}
              </Link>
            );
          }
          return (
            <div
              key={`${name}-${character}-${idx}`}
              data-testid="movie-cast-strip-card"
              data-no-link="true"
              className={cardClass}
            >
              {body}
            </div>
          );
        })}
      </div>
    </section>
  );
}
