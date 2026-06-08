import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Search, X, ChevronDown, ArrowUpDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuCheckboxItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
} from '@/components/ui/dropdown-menu';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';

import type { SeriesCacheStatus, SeriesCacheSort } from '@/lib/api/seriesCache';

export interface SeriesFiltersValue {
  readonly search: string;
  readonly state: SeriesCacheStatus;
  readonly sort: SeriesCacheSort;
  readonly monitoredOnly: boolean;
  readonly networks: ReadonlySet<string>;
}

export interface SeriesFiltersBarProps {
  readonly value: SeriesFiltersValue;
  readonly availableNetworks: readonly string[];
  readonly defaults: SeriesFiltersValue;
  readonly onChange: (next: SeriesFiltersValue) => void;
  readonly onClear: () => void;
}

function isDefault(v: SeriesFiltersValue, d: SeriesFiltersValue): boolean {
  if (v.search !== d.search) return false;
  if (v.state !== d.state) return false;
  if (v.sort !== d.sort) return false;
  if (v.monitoredOnly !== d.monitoredOnly) return false;
  if (v.networks.size !== d.networks.size) return false;
  for (const n of v.networks) if (!d.networks.has(n)) return false;
  return true;
}

const SORT_OPTIONS: readonly SeriesCacheSort[] = ['updated_desc', 'title_asc'];

export function SeriesFiltersBar({
  value, availableNetworks, defaults, onChange, onClear,
}: SeriesFiltersBarProps) {
  const { t } = useTranslation();
  const isAtDefault = useMemo(() => isDefault(value, defaults), [value, defaults]);

  const networksLabel = value.networks.size === 0
    ? t('series.filters.networks.label')
    : t('series.filters.networks.labelWith', { count: value.networks.size });

  const sortLabel = value.sort === 'title_asc'
    ? t('series.filters.sort.titleAsc')
    : t('series.filters.sort.updatedDesc');

  const toggleNetwork = (n: string, checked: boolean) => {
    const next = new Set(value.networks);
    if (checked) next.add(n); else next.delete(n);
    onChange({ ...value, networks: next });
  };

  return (
    <div
      data-testid="series-filters-bar"
      className="flex flex-wrap items-center gap-2 py-2 border-b border-border-faint"
    >
      <div className="relative flex-1 min-w-[200px] max-w-[320px]">
        <Search
          className="absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-tx-faint pointer-events-none"
          aria-hidden="true"
        />
        <Input
          type="search"
          aria-label={t('series.filters.search.aria')}
          placeholder={t('series.filters.search.placeholder')}
          value={value.search}
          onChange={(e) => onChange({ ...value, search: e.target.value })}
          className="pl-7 h-8 text-[12.5px]"
          data-testid="series-filters-search"
        />
      </div>

      <ToggleGroup
        type="single"
        value={value.state}
        onValueChange={(v) => {
          if (v) onChange({ ...value, state: v as SeriesCacheStatus });
        }}
        className="bg-bg-surface border border-border-subtle rounded-md p-0.5"
        data-testid="series-filters-state"
      >
        <ToggleGroupItem value="all" className="text-[12px] px-2.5 h-7">
          {t('series.filters.state.all')}
        </ToggleGroupItem>
        <ToggleGroupItem value="imported" className="text-[12px] px-2.5 h-7">
          {t('series.filters.state.imported')}
        </ToggleGroupItem>
        <ToggleGroupItem value="missing" className="text-[12px] px-2.5 h-7">
          {t('series.filters.state.missing')}
        </ToggleGroupItem>
      </ToggleGroup>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 text-[12.5px]"
            data-testid="series-filters-networks"
          >
            {networksLabel}
            <ChevronDown className="w-3.5 h-3.5 ml-1" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-[280px] overflow-y-auto">
          {availableNetworks.length === 0 ? (
            <div className="px-2 py-1.5 text-[12px] text-tx-faint">
              {t('series.filters.networks.empty')}
            </div>
          ) : (
            availableNetworks.map((n) => (
              <DropdownMenuCheckboxItem
                key={n}
                checked={value.networks.has(n)}
                onCheckedChange={(checked) => toggleNetwork(n, !!checked)}
              >
                {n}
              </DropdownMenuCheckboxItem>
            ))
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <label className="flex items-center gap-1.5 text-[12.5px] text-tx-secondary cursor-pointer">
        <Switch
          checked={value.monitoredOnly}
          onCheckedChange={(c) => onChange({ ...value, monitoredOnly: c })}
          data-testid="series-filters-monitored"
        />
        {t('series.filters.monitoredOnly')}
      </label>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 text-[12.5px]"
            aria-label={t('series.filters.sort.aria')}
            data-testid="series-filters-sort"
          >
            <ArrowUpDown className="w-3.5 h-3.5 mr-1" />
            <span className="truncate">{sortLabel}</span>
            <ChevronDown className="w-3.5 h-3.5 ml-1" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-[180px]">
          <DropdownMenuRadioGroup
            value={value.sort}
            onValueChange={(v) => {
              if (SORT_OPTIONS.includes(v as SeriesCacheSort)) {
                onChange({ ...value, sort: v as SeriesCacheSort });
              }
            }}
          >
            <DropdownMenuRadioItem value="updated_desc" data-testid="series-filters-sort-updated">
              {t('series.filters.sort.updatedDesc')}
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="title_asc" data-testid="series-filters-sort-title">
              {t('series.filters.sort.titleAsc')}
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>

      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onClear}
        disabled={isAtDefault}
        className={cn(
          'h-8 text-[12px] ml-auto',
          isAtDefault && 'opacity-40 pointer-events-none',
        )}
        data-testid="series-filters-clear"
      >
        <X className="w-3.5 h-3.5 mr-1" />
        {t('series.filters.clear')}
      </Button>
    </div>
  );
}
