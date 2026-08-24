import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { MediaImage } from '@/components/MediaImage';

export interface PersonCardProps {
  /** TMDB id → direct link to /person/:tmdbId. */
  readonly tmdbId: number;
  readonly name: string;
  /** Muted subtitle (e.g. "Acting", top billed titles). */
  readonly knownFor?: string | undefined;
  /** Resolved profile media hash → rendered via MediaImage (mediaUrl). */
  readonly profilePath?: string | null | undefined;
  readonly className?: string | undefined;
}

// PersonCard — portrait tile for a person search hit. Mirrors MovieCard's
// aspect-[2/3] markup (poster via MediaImage with a monogram fallback, title
// line + muted subtitle below) but carries no year/rating overlays and links
// to /person/:tmdbId. The profile hash is already resolved by the /search
// media.Resolver, so it flows straight into MediaImage → mediaUrl.
export function PersonCard({ tmdbId, name, knownFor, profilePath, className }: PersonCardProps) {
  const { t } = useTranslation();
  const ariaLabel = t('search.personCard.open', { name });

  return (
    <Link
      to={`/person/${tmdbId}`}
      data-testid="person-card"
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
          hash={profilePath ?? null}
          kind="poster"
          title={name}
          fallback="monogram"
          aspectRatio="aspect-auto"
          className="absolute inset-0"
          data-testid="person-card-poster"
        />
      </div>

      <div className="flex flex-col gap-1 px-0.5 pt-2">
        <div
          data-testid="person-card-name"
          className="truncate text-[13px] font-semibold text-tx-primary"
          title={name}
        >
          {name}
        </div>
        {knownFor && (
          <div
            data-testid="person-card-known-for"
            className="truncate text-[11.5px] text-tx-muted"
            title={knownFor}
          >
            {knownFor}
          </div>
        )}
      </div>
    </Link>
  );
}
