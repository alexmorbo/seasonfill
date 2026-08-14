import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { LanguageFallbackTag } from '@/components/series-detail/LanguageFallbackTag';
import { Skeleton } from '@/components/ui/skeleton';

export interface MovieOverviewBlockProps {
  readonly tmdbId: number;
  // Localized title (dto.MovieOverviewResponse.title). Rendered as the block's
  // sub-heading so the LanguageFallbackTag — which reflects the TITLE's served
  // language — sits next to the text it describes.
  readonly title?: string | undefined;
  // Localized synopsis. Absent/empty → the empty placeholder is shown.
  readonly overview?: string | undefined;
  // Localized tagline. Rendered (italic) only when present.
  readonly tagline?: string | undefined;
  // served_language from the DTO. Feeds LanguageFallbackTag (contentLang) to
  // signal a localized-title fallback — same treatment as MovieCastStrip. The
  // movie /overview `degraded` marker is "missing_lang", which is NOT a member
  // of series' DegradedSource union, so DegradedChip does not apply here.
  readonly servedLanguage?: string | undefined;
  readonly requestedLang?: string | undefined;
  // When true, render a skeleton block instead of content.
  readonly loading?: boolean | undefined;
  readonly className?: string | undefined;
}

export function MovieOverviewBlock({
  tmdbId,
  title,
  overview,
  tagline,
  servedLanguage,
  requestedLang,
  loading,
  className,
}: MovieOverviewBlockProps) {
  const { t } = useTranslation();

  const header = (
    <div
      data-testid="movie-overview-block-header"
      className="flex items-center gap-2.5 mb-3.5 min-w-0"
    >
      <h2
        id="movie-overview-block-heading"
        className="inline-flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint truncate"
      >
        {t('movieDetail.overview.label')}
        <LanguageFallbackTag
          contentLang={servedLanguage}
          {...(requestedLang ? { requestedLang } : {})}
          testid="movie-overview-lang-fallback"
        />
      </h2>
    </div>
  );

  if (loading) {
    return (
      <section
        data-testid="movie-overview-block-loading"
        aria-labelledby="movie-overview-block-heading"
        data-tmdb-id={tmdbId}
        className={cn('flex flex-col gap-3', className)}
      >
        {header}
        <div data-testid="movie-overview-skeleton" className="flex flex-col gap-2">
          <Skeleton className="h-3.5 w-[45%]" />
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-[80%]" />
        </div>
      </section>
    );
  }

  const hasOverview = typeof overview === 'string' && overview.trim().length > 0;

  return (
    <section
      data-testid="movie-overview-block"
      aria-labelledby="movie-overview-block-heading"
      data-tmdb-id={tmdbId}
      className={cn('flex flex-col gap-3', className)}
    >
      {header}
      {title && (
        <h3
          data-testid="movie-overview-title"
          className="text-[15px] font-semibold text-tx-primary leading-snug"
          title={title}
        >
          {title}
        </h3>
      )}
      {tagline && (
        <p
          data-testid="movie-overview-tagline"
          className="text-[13px] italic text-tx-muted leading-snug"
        >
          {tagline}
        </p>
      )}
      {hasOverview ? (
        <p
          data-testid="movie-overview-text"
          className="text-[13.5px] text-tx-secondary leading-relaxed whitespace-pre-line"
        >
          {overview}
        </p>
      ) : (
        <p data-testid="movie-overview-empty" className="text-[13px] text-tx-muted">
          {t('movieDetail.overview.empty')}
        </p>
      )}
    </section>
  );
}
