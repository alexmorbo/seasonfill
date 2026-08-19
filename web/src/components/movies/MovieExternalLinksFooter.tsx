import { ExternalLink } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface MovieExternalLinksFooterProps {
  readonly tmdbId?: number | undefined;
  readonly imdbId?: string | undefined;
  readonly homepage?: string | undefined;
  readonly className?: string | undefined;
}

// Movie analogue of the series ExternalLinksFooter. FORKED (not reused) because
// the series footer hardcodes the TMDB /tv/ path and a TheTVDB entry that do
// not apply to movies — this variant points at /movie/ and drops TVDB. Reuses
// the shared seriesDetail.links.* i18n labels + identical styling. Self-hides
// when no link is available.
export function MovieExternalLinksFooter({
  tmdbId,
  imdbId,
  homepage,
  className,
}: MovieExternalLinksFooterProps) {
  const { t } = useTranslation();
  const entries: Array<{ key: string; label: string; href: string }> = [];
  if (imdbId) {
    entries.push({
      key: 'imdb',
      label: t('seriesDetail.links.imdb'),
      href: `https://www.imdb.com/title/${imdbId}/`,
    });
  }
  if (typeof tmdbId === 'number' && tmdbId > 0) {
    entries.push({
      key: 'tmdb',
      label: t('seriesDetail.links.tmdb'),
      href: `https://www.themoviedb.org/movie/${tmdbId}`,
    });
  }
  if (homepage) {
    entries.push({
      key: 'homepage',
      label: t('seriesDetail.links.homepage'),
      href: homepage,
    });
  }
  if (entries.length === 0) return null;
  return (
    <div
      data-testid="movie-external-links-footer"
      className={cn(
        'flex flex-wrap items-center gap-x-4 gap-y-2 text-[12px] text-tx-muted pt-4 border-t border-border-faint/60',
        className,
      )}
    >
      {entries.map((e) => (
        <a
          key={e.key}
          href={e.href}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 hover:text-tx-primary transition-colors"
        >
          <ExternalLink className="w-3 h-3" aria-hidden="true" />
          {e.label}
        </a>
      ))}
    </div>
  );
}
