import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { SortControl } from './SortControl';
import { SeriesCard } from '@/components/series/SeriesCard';
import type { LibraryCreditEntry, LibrarySort } from '@/api/person';

export interface LibraryCreditsGridProps {
  readonly credits: readonly LibraryCreditEntry[];
  readonly sort: LibrarySort;
  readonly onSortChange: (next: LibrarySort) => void;
  readonly className?: string | undefined;
}

export function LibraryCreditsGrid({
  credits,
  sort,
  onSortChange,
  className,
}: LibraryCreditsGridProps) {
  const { t } = useTranslation();

  if (credits.length === 0) return null;

  return (
    <section
      data-testid="person-library-section"
      className={cn('flex flex-col gap-3', className)}
    >
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <h2 className="text-[15px] font-semibold text-tx-primary">
          {t('person.library.heading', { count: credits.length })}
        </h2>
        <SortControl value={sort} onChange={onSortChange} />
      </div>

      <div
        data-testid="person-library-grid"
        className="grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5"
      >
        {credits.map((c) => {
          // Instances are already sorted alphabetically by BE; pick the
          // first as a stable display label for the footer.
          const primary = (c.instances ?? [])[0];
          const instance = primary?.instance;
          const role = c.role_label ?? c.character_name ?? '';
          const hasSeriesId = typeof c.series_id === 'number' && c.series_id > 0;
          const key = `${c.series_id ?? 'x'}-${instance ?? 'noinst'}`;
          const footer = instance ? (
            <div
              data-testid="person-library-instance"
              className="text-[10.5px] text-tx-faint font-mono uppercase tracking-wide truncate"
              title={instance}
            >
              {instance}
            </div>
          ) : null;
          return (
            <SeriesCard
              key={key}
              title={c.title ?? ''}
              year={c.year ?? undefined}
              rating={c.tmdb_rating ?? undefined}
              posterAsset={c.poster_asset ?? undefined}
              seriesId={hasSeriesId ? c.series_id : undefined}
              characterName={role || undefined}
              libraryBadge="inLibrary"
              footer={footer}
            />
          );
        })}
      </div>
    </section>
  );
}
