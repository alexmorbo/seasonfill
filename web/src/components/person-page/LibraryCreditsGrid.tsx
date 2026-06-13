import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/seriesDetail';
import { SortControl } from './SortControl';
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
          const src = mediaUrl(c.poster_asset);
          const instance = (c.instances ?? [])[0];
          const seriesId = c.series_id;
          const role = c.role_label ?? c.character_name ?? '';
          const titleYear = c.year ? `${c.title ?? ''} · ${c.year}` : (c.title ?? '');
          const key = `${seriesId ?? 'x'}-${instance ?? 'noinst'}`;

          const inner = (
            <div className="flex flex-col gap-1.5 p-2 rounded-lg border border-border-subtle bg-bg-surface hover:border-accent/40 transition-colors h-full">
              <div className="aspect-[2/3] w-full rounded overflow-hidden bg-bg-surface-2 border border-border-subtle">
                {src && (
                  <img
                    src={src}
                    alt=""
                    aria-hidden="true"
                    loading="lazy"
                    decoding="async"
                    className="w-full h-full object-cover"
                  />
                )}
              </div>
              <div className="text-[12.5px] font-semibold text-tx-primary truncate" title={c.title}>
                {titleYear}
              </div>
              {role && (
                <div className="text-[11.5px] text-tx-muted truncate" title={role}>
                  {role}
                </div>
              )}
              {instance && (
                <div className="text-[10.5px] text-tx-faint font-mono uppercase tracking-wide truncate">
                  {instance}
                </div>
              )}
            </div>
          );

          return instance && seriesId ? (
            <Link
              key={key}
              to={`/series/${encodeURIComponent(instance)}/${seriesId}`}
              data-testid="person-library-card"
              data-instance={instance}
              data-series-id={seriesId}
              className="block focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg"
            >
              {inner}
            </Link>
          ) : (
            <div key={key} data-testid="person-library-card" data-instance="" data-series-id="">
              {inner}
            </div>
          );
        })}
      </div>
    </section>
  );
}
