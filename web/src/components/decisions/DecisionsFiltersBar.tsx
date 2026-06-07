import { useTranslation } from 'react-i18next';
import { Search, RotateCcw } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';
import {
  REASON_CATEGORY_OPTIONS,
  REASON_CATEGORY_DOT_CLASS,
  type ReasonCategoryKey,
} from '@/lib/decisions/reasonCategory';
import type {
  DecisionsWindow, DecisionsSort,
} from '@/lib/api/decisions';

export type CategoryFilter = ReasonCategoryKey | 'all';

export interface DecisionsFiltersBarProps {
  readonly search: string;
  readonly category: CategoryFilter;
  readonly instance: string | null;
  readonly availableInstances: readonly string[];
  readonly window: DecisionsWindow;
  readonly sort: DecisionsSort;
  readonly counts: Readonly<Record<CategoryFilter, number>>;
  readonly onSearchChange: (next: string) => void;
  readonly onCategoryChange: (next: CategoryFilter) => void;
  readonly onInstanceChange: (next: string | null) => void;
  readonly onWindowChange: (next: DecisionsWindow) => void;
  readonly onSortChange: (next: DecisionsSort) => void;
  readonly onReset: () => void;
  readonly canReset: boolean;
}

const WINDOW_OPTIONS: readonly DecisionsWindow[] = ['24h', '7d', '30d', 'all'] as const;
const SORT_OPTIONS: readonly DecisionsSort[] = ['freshest', 'stuck-first'] as const;

export function DecisionsFiltersBar(props: DecisionsFiltersBarProps) {
  const { t } = useTranslation();
  const {
    search, category, instance, availableInstances, window, sort, counts,
    onSearchChange, onCategoryChange, onInstanceChange, onWindowChange,
    onSortChange, onReset, canReset,
  } = props;

  return (
    <div className="flex items-center gap-2.5 flex-wrap mb-4">
      <div className="relative flex-1 min-w-[200px]">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-tx-muted pointer-events-none" />
        <Input
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder={t('decisions.filters.searchPlaceholder')}
          className="h-8 pl-8 text-[12.5px]"
          aria-label={t('decisions.filters.searchAria')}
        />
      </div>

      {/* Category (Результат) */}
      <Select
        value={category}
        onValueChange={(v) => { if (v) onCategoryChange(v as CategoryFilter); }}
      >
        <SelectTrigger className="h-8 w-[200px] text-[12.5px]" aria-label={t('decisions.filters.categoryAria')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">
            <span className="inline-flex items-center gap-2">
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-tx-faint" />
              {t('decisions.filters.category.all')}
              <span className="ml-2 font-mono text-[10.5px] text-tx-faint">{counts.all}</span>
            </span>
          </SelectItem>
          {REASON_CATEGORY_OPTIONS.map((opt) => (
            <SelectItem key={opt} value={opt}>
              <span className="inline-flex items-center gap-2">
                <span className={cn('inline-block w-1.5 h-1.5 rounded-full', REASON_CATEGORY_DOT_CLASS[opt])} />
                {t(`decisions.filters.category.${opt}`)}
                <span className="ml-2 font-mono text-[10.5px] text-tx-faint">{counts[opt]}</span>
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {/* Instance */}
      <Select
        value={instance ?? '__all__'}
        onValueChange={(v) => { if (v) onInstanceChange(v === '__all__' ? null : v); }}
      >
        <SelectTrigger className="h-8 w-[140px] text-[12.5px]" aria-label={t('decisions.filters.instanceAria')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="__all__">{t('decisions.filters.instance.all')}</SelectItem>
          {availableInstances.map((name) => (
            <SelectItem key={name} value={name}>{name}</SelectItem>
          ))}
        </SelectContent>
      </Select>

      {/* Window */}
      <Select
        value={window}
        onValueChange={(v) => { if (v) onWindowChange(v as DecisionsWindow); }}
      >
        <SelectTrigger className="h-8 w-[120px] text-[12.5px]" aria-label={t('decisions.filters.windowAria')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {WINDOW_OPTIONS.map((w) => (
            <SelectItem key={w} value={w}>{t(`decisions.window.${w === '24h' ? 'h24' : w === '7d' ? 'd7' : w === '30d' ? 'd30' : 'all'}`)}</SelectItem>
          ))}
        </SelectContent>
      </Select>

      {/* Sort */}
      <Select
        value={sort}
        onValueChange={(v) => { if (v) onSortChange(v as DecisionsSort); }}
      >
        <SelectTrigger className="h-8 w-[160px] text-[12.5px]" aria-label={t('decisions.filters.sortAria')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {SORT_OPTIONS.map((s) => (
            <SelectItem key={s} value={s}>{t(`decisions.filters.sort.${s === 'stuck-first' ? 'stuckFirst' : 'freshest'}`)}</SelectItem>
          ))}
        </SelectContent>
      </Select>

      <Button
        variant="ghost" size="sm"
        onClick={onReset}
        disabled={!canReset}
        className="gap-1.5"
        aria-label={t('decisions.filters.reset')}
      >
        <RotateCcw className="size-3.5" />
        {t('decisions.filters.reset')}
      </Button>
    </div>
  );
}
