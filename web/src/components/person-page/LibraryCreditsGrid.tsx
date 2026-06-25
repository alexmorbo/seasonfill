import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { SortControl } from './SortControl';
import { CreditCard, type CreditLinkTarget } from './CreditCard';
import type { LibraryCreditEntry, LibrarySort } from '@/api/person';

export interface LibraryCreditsGridProps {
  readonly credits: readonly LibraryCreditEntry[];
  readonly sort: LibrarySort;
  readonly onSortChange: (next: LibrarySort) => void;
  readonly className?: string | undefined;
}

/**
 * libraryLinkTarget — Story 537 (B-42e). After Story 536 the
 * canonical URL is /series/{canonical_series_id} — `series_id`
 * is always populated on library credits (BE wire confirms it).
 * The legacy 3-segment `/series/{instance}/{sonarrId}` form is
 * dead (LegacySeriesRedirect now strips it down to the wrong id
 * because Sonarr per-instance ids ≠ canon series.id for nearly
 * every row).
 */
function libraryLinkTarget(c: LibraryCreditEntry): CreditLinkTarget {
  if (typeof c.series_id === 'number' && c.series_id > 0) {
    return { kind: 'internal', to: `/series/${c.series_id}` };
  }
  // series_id ABSENT on a library credit indicates a contract
  // violation (BE always populates it). Defensive fallback: render
  // as a non-clickable card so we don't ship a broken link.
  return { kind: 'none' };
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
          const link = libraryLinkTarget(c);
          // Instances are already sorted alphabetically by BE; pick the
          // first as a stable display label for the footer.
          const primary = (c.instances ?? [])[0];
          const instance = primary?.instance;
          const role = c.role_label ?? c.character_name ?? '';
          const key = `${c.series_id ?? 'x'}-${instance ?? 'noinst'}`;
          const footer = instance ? (
            <div
              className="text-[10.5px] text-tx-faint font-mono uppercase tracking-wide truncate"
              title={instance}
            >
              {instance}
            </div>
          ) : null;
          return (
            <CreditCard
              key={key}
              testId="person-library-card"
              title={c.title ?? ''}
              year={c.year ?? undefined}
              role={role || undefined}
              posterAsset={c.poster_asset ?? undefined}
              badge="inLibrary"
              link={link}
              footer={footer}
              dataAttrs={{
                'series-id': c.series_id ?? undefined,
                instance: instance ?? undefined,
                'sonarr-id': primary?.sonarr_series_id ?? undefined,
              }}
            />
          );
        })}
      </div>
    </section>
  );
}
