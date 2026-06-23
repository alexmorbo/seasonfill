import { ExternalLink } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import type { ExternalLinks } from '@/api/series';

export interface ExternalLinksFooterProps {
  readonly links?: ExternalLinks | undefined;
  readonly className?: string | undefined;
}

export function ExternalLinksFooter({ links, className }: ExternalLinksFooterProps) {
  const { t } = useTranslation();
  if (!links) return null;
  const entries: Array<{ key: string; label: string; href: string }> = [];
  if (links.imdb_id) entries.push({ key: 'imdb', label: t('seriesDetail.links.imdb'), href: `https://www.imdb.com/title/${links.imdb_id}/` });
  if (links.tmdb_id) entries.push({ key: 'tmdb', label: t('seriesDetail.links.tmdb'), href: `https://www.themoviedb.org/tv/${links.tmdb_id}` });
  if (links.tvdb_id) entries.push({ key: 'tvdb', label: t('seriesDetail.links.tvdb'), href: `https://thetvdb.com/?id=${links.tvdb_id}&tab=series` });
  if (links.homepage) entries.push({ key: 'homepage', label: t('seriesDetail.links.homepage'), href: links.homepage });
  if (entries.length === 0) return null;
  return (
    <div
      data-testid="external-links-footer"
      className={cn('flex flex-wrap items-center gap-x-4 gap-y-2 text-[12px] text-tx-muted pt-4 border-t border-border-faint/60', className)}
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
