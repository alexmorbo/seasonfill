import { useTranslation } from 'react-i18next';
import { Search, Server } from 'lucide-react';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export type GrabFilter = 'all' | 'active' | 'history' | 'fails';

export interface GrabsFiltersBarProps {
  filter: GrabFilter;
  onFilterChange: (next: GrabFilter) => void;
  counts: { all: number; active: number; history: number; fails: number };
  search: string;
  onSearchChange: (next: string) => void;
  instance: string | null;
}

export function GrabsFiltersBar({
  filter, onFilterChange, counts, search, onSearchChange, instance,
}: GrabsFiltersBarProps) {
  const { t } = useTranslation();
  const items: Array<{ key: GrabFilter; danger?: boolean }> = [
    { key: 'all' },
    { key: 'active' },
    { key: 'history' },
    { key: 'fails', danger: true },
  ];
  return (
    <div className="flex items-center gap-2.5 flex-wrap mb-4">
      <ToggleGroup
        type="single"
        value={filter}
        onValueChange={(v) => {
          // Radix Select / ToggleGroup may emit '' when the user clicks
          // the active item again. Guard against that — we always need
          // a selected filter.
          if (v) onFilterChange(v as GrabFilter);
        }}
      >
        {items.map(({ key, danger }) => (
          <ToggleGroupItem
            key={key}
            value={key}
            className={cn(
              'gap-1.5',
              danger && filter === key && 'data-[state=on]:text-danger',
            )}
            aria-label={t(`grabs.filter.${key}`)}
          >
            {t(`grabs.filter.${key}`)}
            <span className="font-mono text-[10.5px] text-tx-faint">
              {counts[key]}
            </span>
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <div className="flex-1" />
      <div className="relative">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-tx-muted pointer-events-none" />
        <Input
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder={t('grabs.search.placeholder')}
          className="h-8 w-[240px] pl-8 text-[12.5px]"
          aria-label={t('grabs.search.placeholder')}
        />
      </div>
      {instance && (
        <Button variant="outline" size="sm" disabled className="gap-1.5">
          <Server className="size-3.5" />
          {instance}
        </Button>
      )}
    </div>
  );
}
