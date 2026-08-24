import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { MediaImage } from '@/components/MediaImage';

export interface CollectionCardProps {
  /** TMDB collection id → direct link to /collections/:tmdbId. */
  readonly tmdbId: number;
  readonly name: string;
  /** Resolved poster media hash (sha256) → rendered via MediaImage
   *  (mediaUrl), same /api/v1/media/{hash} path as MovieCard. The raw
   *  hash flows straight in — NOT pre-wrapped through resolveSearchPoster
   *  (that would double-encode; the BE already returns hashes). */
  readonly poster?: string | null | undefined;
  readonly className?: string | undefined;
}

// CollectionCard — portrait tile for a TMDB-collection search hit. Mirrors
// MovieCard's aspect-[2/3] poster markup but carries NO year/rating overlays
// (collections have no year) and links to /collections/:tmdbId.
export function CollectionCard({ tmdbId, name, poster, className }: CollectionCardProps) {
  const { t } = useTranslation();
  const ariaLabel = t('collectionCard.open', { name });

  return (
    <Link
      to={`/collections/${tmdbId}`}
      data-testid="collection-card"
      data-tmdb-id={tmdbId}
      aria-label={ariaLabel}
      className={cn(
        'group relative block',
        'transition-transform duration-150 ease-out hover:-translate-y-0.5',
        'focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg',
        className,
      )}
    >
      <div className="relative aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-bg-surface-2">
        <MediaImage
          hash={poster ?? null}
          kind="poster"
          title={name}
          fallback="monogram"
          aspectRatio="aspect-auto"
          className="absolute inset-0"
          data-testid="collection-card-poster"
        />
      </div>

      <div className="flex flex-col gap-1 px-0.5 pt-2">
        <div
          data-testid="collection-card-name"
          className="truncate text-[13px] font-semibold text-tx-primary"
          title={name}
        >
          {name}
        </div>
      </div>
    </Link>
  );
}
