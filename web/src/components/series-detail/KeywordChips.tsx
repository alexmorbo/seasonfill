import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import type { components } from '@/api/schema';

type TaxonomyChip = components['schemas']['dto.TaxonomyChip'];

export interface KeywordChipsProps {
  readonly chips?: readonly TaxonomyChip[] | undefined;
  readonly limit?: number;
  readonly className?: string | undefined;
}

export function KeywordChips({ chips, limit = 12, className }: KeywordChipsProps) {
  const { t } = useTranslation();
  const items = (chips ?? []).slice(0, limit);
  if (items.length === 0) return null;
  return (
    <div
      data-testid="keyword-chips"
      role="list"
      aria-label={t('seriesDetail.overview.keywords')}
      className={cn('flex flex-wrap gap-1.5', className)}
    >
      {items.map((k) => (
        <span
          key={k.id ?? k.name}
          role="listitem"
          data-testid="keyword-chip"
          className="rounded-md bg-bg-surface-2/70 border border-border-subtle px-1.5 py-0.5 text-[11px] text-tx-secondary"
        >
          {k.name}
        </span>
      ))}
    </div>
  );
}
