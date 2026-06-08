import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Search, X, ChevronDown, ArrowUpDown, RotateCcw, Check } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
} from '@/components/ui/dropdown-menu';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';

import type { SeriesCacheStatus, SeriesCacheSort } from '@/lib/api/seriesCache';
import { isDefaultFilters, type SeriesFiltersValue } from './seriesFilters';

export type { SeriesFiltersValue } from './seriesFilters';

export interface SeriesFiltersBarProps {
  readonly value: SeriesFiltersValue;
  readonly availableNetworks: readonly string[];
  readonly defaults: SeriesFiltersValue;
  readonly onChange: (next: SeriesFiltersValue) => void;
  readonly onClear: () => void;
}

const SORT_OPTIONS: readonly SeriesCacheSort[] = ['updated_desc', 'title_asc', 'air_date_desc'];

function resolveSortLabel(
  sort: SeriesCacheSort,
  t: (k: string) => string,
): string {
  switch (sort) {
    case 'title_asc':
      return t('series.filters.sort.titleAsc');
    case 'air_date_desc':
      return t('series.filters.sort.airDateDesc');
    default:
      return t('series.filters.sort.updatedDesc');
  }
}

export function SeriesFiltersBar({
  value, availableNetworks, defaults, onChange, onClear,
}: SeriesFiltersBarProps) {
  const { t } = useTranslation();
  const isAtDefault = useMemo(() => isDefaultFilters(value, defaults), [value, defaults]);

  const networksLabel = value.networks.size === 0
    ? t('series.filters.networks.label')
    : t('series.filters.networks.labelWith', { count: value.networks.size });

  const sortLabel = resolveSortLabel(value.sort, t);

  const toggleNetwork = (n: string, checked: boolean) => {
    const next = new Set(value.networks);
    if (checked) next.add(n); else next.delete(n);
    onChange({ ...value, networks: next });
  };

  const [networkQuery, setNetworkQuery] = useState('');
  const filteredNetworks = useMemo(() => {
    const q = networkQuery.trim().toLowerCase();
    if (!q) return availableNetworks;
    return availableNetworks.filter((n) => n.toLowerCase().includes(q));
  }, [networkQuery, availableNetworks]);
  const selectedNetworks = useMemo(
    () => [...value.networks].sort((a, b) => a.localeCompare(b)),
    [value.networks],
  );

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
            className={cn(
              'h-8 text-[12.5px]',
              value.networks.size > 0
                && 'bg-accent-dim text-accent border border-accent/35',
            )}
            data-testid="series-filters-networks"
          >
            {networksLabel}
            <ChevronDown className="w-3.5 h-3.5 ml-1" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="start"
          className="max-h-[320px] w-[260px] overflow-hidden p-0"
          data-testid="series-filters-networks-content"
        >
          {availableNetworks.length === 0 ? (
            <div className="px-2 py-1.5 text-[12px] text-tx-faint">
              {t('series.filters.networks.empty')}
            </div>
          ) : (
            <div className="flex flex-col">
              {selectedNetworks.length > 0 && (
                <div
                  className="flex flex-wrap gap-1 p-2 border-b border-border-subtle"
                  data-testid="series-filters-networks-chips"
                >
                  {selectedNetworks.map((n) => (
                    <button
                      key={n}
                      type="button"
                      onClick={(e) => {
                        e.preventDefault();
                        toggleNetwork(n, false);
                      }}
                      className="inline-flex items-center gap-1 rounded-md border border-accent/35 bg-accent-dim text-accent px-1.5 py-0.5 text-[11.5px] hover:bg-accent-dim/70"
                      data-testid={`series-filters-networks-chip-${n}`}
                      aria-label={t('series.filters.networks.removeChip', { network: n })}
                    >
                      <span className="truncate max-w-[140px]">{n}</span>
                      <X className="w-3 h-3" aria-hidden="true" />
                    </button>
                  ))}
                </div>
              )}
              <div className="p-2 border-b border-border-subtle">
                <div className="relative">
                  <Search
                    className="absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-tx-faint pointer-events-none"
                    aria-hidden="true"
                  />
                  <input
                    type="text"
                    value={networkQuery}
                    onChange={(e) => setNetworkQuery(e.target.value)}
                    onKeyDown={(e) => e.stopPropagation()}
                    placeholder={t('series.filters.networks.searchPlaceholder')}
                    aria-label={t('series.filters.networks.searchAria')}
                    className="w-full h-7 pl-7 pr-2 rounded-md border border-border-subtle bg-bg-base text-[12px] outline-hidden focus:border-accent/50"
                    data-testid="series-filters-networks-search"
                  />
                </div>
              </div>
              <div
                className="max-h-[200px] overflow-y-auto py-1"
                data-testid="series-filters-networks-list"
              >
                {filteredNetworks.length === 0 ? (
                  <div className="px-2 py-1.5 text-[12px] text-tx-faint">
                    {t('series.filters.networks.noMatches')}
                  </div>
                ) : (
                  filteredNetworks.map((n) => {
                    const checked = value.networks.has(n);
                    return (
                      <button
                        key={n}
                        type="button"
                        onClick={(e) => {
                          e.preventDefault();
                          toggleNetwork(n, !checked);
                        }}
                        className={cn(
                          'flex w-full items-center gap-2 px-2 py-1.5 text-left text-[12.5px]',
                          'hover:bg-bg-surface-2',
                          checked && 'text-accent',
                        )}
                        data-testid={`series-filters-networks-item-${n}`}
                        aria-pressed={checked}
                      >
                        <span className="flex w-3.5 h-3.5 items-center justify-center text-accent">
                          {checked && <Check className="w-3.5 h-3.5" aria-hidden="true" />}
                        </span>
                        <span className="truncate">{n}</span>
                      </button>
                    );
                  })
                )}
              </div>
            </div>
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
            <DropdownMenuRadioItem value="air_date_desc" data-testid="series-filters-sort-air-date">
              {t('series.filters.sort.airDateDesc')}
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>

      {!isAtDefault && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onClear}
          className="h-8 text-[12px] ml-auto bg-accent-dim text-accent border border-accent/35 hover:bg-accent-dim/70"
          data-testid="series-filters-clear"
        >
          <RotateCcw className="w-3.5 h-3.5 mr-1" />
          {t('series.filters.clearAll')}
        </Button>
      )}
    </div>
  );
}
