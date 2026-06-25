import { useTranslation } from 'react-i18next';
import { Tag } from 'lucide-react';
import { useDiscoveryGenresList } from '@/api/discovery';
import { Skeleton } from '@/components/ui/skeleton';
import { EmptyState } from '@/components/EmptyState';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { toBcp47 } from '@/lib/locale';
import { cn } from '@/lib/utils';

export interface GenreFilterProps {
  readonly selectedGenreId: number | null;
  readonly onSelect: (id: number | null) => void;
}

const CHIP_BASE = cn(
  'inline-flex items-center rounded-full border px-3 py-1 text-[12.5px] font-medium',
  'transition-colors focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent',
);

// Story 515 / N-3c: horizontal chip strip of TMDB genres. Click toggles
// the selection; parent renders <GenreResultsGrid /> below.
export function GenreFilter({ selectedGenreId, onSelect }: GenreFilterProps) {
  const { t, i18n } = useTranslation();
  const q = useDiscoveryGenresList(toBcp47(i18n.resolvedLanguage));

  if (q.isPending) {
    return (
      <div
        className="mb-4 flex flex-wrap gap-2"
        data-testid="discovery-genres-skeleton"
      >
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-7 w-20 rounded-full" />
        ))}
      </div>
    );
  }

  if (q.isError) {
    return (
      <Alert variant="destructive" data-testid="discovery-genres-error">
        <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
      </Alert>
    );
  }

  const items = q.data?.items ?? [];
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Tag className="h-7 w-7" />}
        title={t('discovery.tabs.genres')}
      />
    );
  }

  return (
    <div className="mb-4 flex flex-wrap gap-2" data-testid="discovery-genres-chips">
      {items.map((g) => {
        const active = g.id === selectedGenreId;
        return (
          <button
            key={g.id} type="button" data-testid="discovery-genre-chip"
            data-genre-id={g.id} aria-pressed={active}
            onClick={() => onSelect(active ? null : g.id)}
            className={cn(CHIP_BASE, active
              ? 'border-accent bg-accent/15 text-accent'
              : 'border-border-faint text-tx-muted hover:text-tx-primary')}
          >
            {g.name}
          </button>
        );
      })}
    </div>
  );
}
