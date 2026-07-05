import type { KeyboardEvent, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Star } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/series';
import { formatSeriesTitle } from '@/lib/title';
import { MediaImage } from '@/components/MediaImage';
import { AddToSonarrButton } from '@/components/discovery/AddToSonarrButton';
import type { DiscoverySeriesItem } from '@/api/discovery';
import { useResolveSeriesNav } from './useResolveSeriesNav';

export interface SeriesCardProps {
  readonly title: string;
  readonly year?: number | undefined;
  /** ★ shown only when a positive number. Absent/0 → year alone, no star. */
  readonly rating?: number | undefined;
  /** Content-addressed poster hash → rendered via MediaImage (library/dashboard). */
  readonly posterHash?: string | null | undefined;
  /** Poster asset path → rendered via mediaUrl (discovery/recs/person). */
  readonly posterAsset?: string | null | undefined;
  /** Canonical series id → direct link to /series/:id. */
  readonly seriesId?: number | undefined;
  /** TMDB id → click resolves to a canonical id, then navigates. */
  readonly tmdbId?: number | undefined;
  /** "N missing" chip (top-right). Rendered only when > 0. */
  readonly missingCount?: number | undefined;
  /** "In library" badge (top-left). */
  readonly libraryBadge?: 'inLibrary' | null | undefined;
  /** Discovery item → renders the Add-to-Sonarr button when not in library. */
  readonly addToSonarr?: DiscoverySeriesItem | undefined;
  /** Muted role line under the title (person page). */
  readonly characterName?: string | undefined;
  /** Extra slot below the meta line (instance label / dept pill). */
  readonly footer?: ReactNode | undefined;
  readonly className?: string | undefined;
}

function Poster({
  posterHash,
  posterAsset,
  title,
}: {
  readonly posterHash?: string | null | undefined;
  readonly posterAsset?: string | null | undefined;
  readonly title: string;
}) {
  if (posterHash != null) {
    return (
      <MediaImage
        hash={posterHash}
        kind="series_poster"
        title={title}
        fallback="monogram"
        aspectRatio="aspect-auto"
        className="absolute inset-0"
      />
    );
  }
  const src = mediaUrl(posterAsset ?? undefined);
  if (src) {
    return (
      <img
        src={src}
        alt=""
        aria-hidden="true"
        loading="lazy"
        decoding="async"
        data-testid="series-card-poster-img"
        className="absolute inset-0 h-full w-full object-cover"
      />
    );
  }
  return (
    <div
      data-testid="series-card-poster-fallback"
      className="absolute inset-0 flex items-center justify-center bg-bg-surface-2 text-tx-faint"
    >
      <span className="text-2xl font-semibold">
        {(title.charAt(0) || '?').toUpperCase()}
      </span>
    </div>
  );
}

// SeriesCard — the single portrait card unified across the list, discovery,
// recommendations and person surfaces. Base render is poster + title + a meta
// line (year + optional ★rating). Every surface-specific affordance is an
// opt-in slot (missing chip, in-library badge, Add-to-Sonarr, role line,
// footer). Clicks always route internally to /series/:id via
// useResolveSeriesNav (direct when seriesId is known, resolve-then-navigate
// when only a tmdbId is available).
export function SeriesCard({
  title,
  year,
  rating,
  posterHash,
  posterAsset,
  seriesId,
  tmdbId,
  missingCount,
  libraryBadge,
  addToSonarr,
  characterName,
  footer,
  className,
}: SeriesCardProps) {
  const { t } = useTranslation();
  const { resolveAndNavigate, pending } = useResolveSeriesNav();

  const hasDirectId = typeof seriesId === 'number' && seriesId > 0;
  const hasTmdb = typeof tmdbId === 'number' && tmdbId > 0;
  const showRating = typeof rating === 'number' && rating > 0;
  const showMeta = year !== undefined || showRating;
  const ariaLabel = t('seriesCard.open', {
    title: formatSeriesTitle(title, year),
  });

  const body = (
    <>
      <div className="relative aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-bg-surface-2">
        <Poster posterHash={posterHash} posterAsset={posterAsset} title={title} />

        {libraryBadge === 'inLibrary' && (
          <span
            data-testid="series-card-library-badge"
            className="absolute left-2 top-2 z-20 inline-flex items-center rounded-full bg-accent/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent backdrop-blur-sm"
          >
            {t('discovery.in_library')}
          </span>
        )}

        {typeof missingCount === 'number' && missingCount > 0 && (
          <span
            data-testid="series-card-missing-chip"
            className="absolute right-2 top-2 z-20 inline-flex items-center gap-1 rounded-full bg-warn/90 px-2 py-0.5 text-[10.5px] font-semibold text-bg-base backdrop-blur-sm"
          >
            {t('series.tile.missing', { count: missingCount })}
          </span>
        )}

        {addToSonarr && (
          <div className="absolute right-2 top-2 z-20">
            <AddToSonarrButton item={addToSonarr} />
          </div>
        )}
      </div>

      <div className="flex flex-col gap-1 px-0.5 pt-2">
        <div
          data-testid="series-card-title"
          className="truncate text-[13px] font-semibold text-tx-primary"
          title={title}
        >
          {title}
        </div>
        {characterName && (
          <div
            data-testid="series-card-character"
            className="truncate text-[11.5px] text-tx-muted"
            title={characterName}
          >
            {characterName}
          </div>
        )}
        {showMeta && (
          <div className="flex items-center gap-1.5 text-[11px] text-tx-muted tabular-nums">
            {year !== undefined && <span>{year}</span>}
            {showRating && (
              <span
                data-testid="series-card-rating"
                className="inline-flex items-center gap-0.5"
              >
                <Star
                  className="h-2.5 w-2.5 text-warn"
                  aria-hidden="true"
                  fill="currentColor"
                />
                {rating.toFixed(1)}
              </span>
            )}
          </div>
        )}
        {footer}
      </div>
    </>
  );

  const rootClass = cn(
    'group relative block',
    'transition-transform duration-150 ease-out hover:-translate-y-0.5',
    'focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg',
    className,
  );

  if (hasDirectId) {
    return (
      <Link
        to={`/series/${seriesId}`}
        data-testid="series-card"
        data-series-id={seriesId}
        aria-label={ariaLabel}
        className={rootClass}
      >
        {body}
      </Link>
    );
  }

  if (hasTmdb) {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        void resolveAndNavigate({ tmdbId });
      }
    };
    return (
      <div
        role="button"
        tabIndex={0}
        onClick={() => void resolveAndNavigate({ tmdbId })}
        onKeyDown={onKey}
        data-testid="series-card"
        data-tmdb-id={tmdbId}
        aria-label={ariaLabel}
        aria-busy={pending}
        className={cn(rootClass, 'cursor-pointer')}
      >
        {body}
      </div>
    );
  }

  return (
    <div data-testid="series-card" className={rootClass}>
      {body}
    </div>
  );
}
