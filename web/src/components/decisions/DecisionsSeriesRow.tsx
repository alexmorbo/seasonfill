import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import { CategoryChip } from '@/components/CategoryChip';
import type { CategoryKind } from '@/lib/decision-category';

export interface DecisionsSeriesRowProps {
  readonly seriesTitle: string;
  readonly worstCategory: CategoryKind;
  readonly seasonCount: number;
  readonly stuckCycles?: number;
  readonly open: boolean;
}

export function DecisionsSeriesRow({
  seriesTitle, worstCategory, seasonCount, stuckCycles, open,
}: DecisionsSeriesRowProps) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-2.5 w-full text-left">
      <ChevronDown
        className={cn(
          'size-4 text-tx-muted transition-transform duration-200 shrink-0',
          !open && '-rotate-90',
        )}
        aria-hidden="true"
      />
      <span className="font-semibold text-[14px] truncate">
        {seriesTitle}
      </span>
      <CategoryChip value={worstCategory} variant="compact" />
      <span className="flex-1" />
      <span className="font-mono text-[11.5px] text-tx-faint">
        {stuckCycles && stuckCycles > 0
          ? t('decisions.series.countStuck', {
              seasons: seasonCount, cycles: stuckCycles,
            })
          : t('decisions.series.count', { seasons: seasonCount })}
      </span>
    </div>
  );
}
