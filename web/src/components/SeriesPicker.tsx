import {
  useCallback, useEffect, useMemo, useRef, useState,
  type ReactNode, type KeyboardEvent, type ChangeEvent,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, X, Search } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { useSeriesSearch, type SeriesSearchItem } from '@/lib/series-search';
import { cn } from '@/lib/utils';

export interface SeriesPickerProps {
  readonly instance: string;
  readonly value: ReadonlyArray<number>;
  readonly onChange: (next: ReadonlyArray<number>) => void;
  readonly onLoadingChange?: (loading: boolean) => void;
  readonly disabled?: boolean;
  readonly placeholder?: string;
  readonly helperText?: ReactNode;
  readonly className?: string;
}

const DEBOUNCE_MS = 250;  // Q-013b-1
const MAX_VISIBLE = 8;

export function SeriesPicker({
  instance, value, onChange, onLoadingChange,
  disabled, placeholder, helperText, className,
}: SeriesPickerProps) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(-1);
  const titleCache = useRef(new Map<number, string>());

  // 250ms debounce — setTimeout (not requestIdleCallback) for
  // predictable behavior under fake timers in tests.
  useEffect(() => {
    const h = setTimeout(() => setDebouncedQuery(query), DEBOUNCE_MS);
    return () => clearTimeout(h);
  }, [query]);

  const search = useSeriesSearch({
    instance, query: debouncedQuery,
    enabled: open && instance.length > 0,
  });

  // Q-013b-4: hide already-selected.
  const visible = useMemo<readonly SeriesSearchItem[]>(() => {
    const items = search.data?.items ?? [];
    return items
      .filter((s) => s.series_id !== undefined && !value.includes(s.series_id))
      .slice(0, MAX_VISIBLE);
  }, [search.data, value]);

  // Cache titles for chip rendering across query changes.
  useEffect(() => {
    for (const it of search.data?.items ?? []) {
      if (it.series_id !== undefined && it.title) {
        titleCache.current.set(it.series_id, it.title);
      }
    }
  }, [search.data]);

  useEffect(() => {
    if (activeIndex >= visible.length) setActiveIndex(visible.length - 1);
  }, [visible.length, activeIndex]);

  const isLoading = search.isFetching || query !== debouncedQuery;

  useEffect(() => {
    onLoadingChange?.(isLoading);
  }, [isLoading, onLoadingChange]);

  const hasError = search.isError;

  const pick = useCallback((item: SeriesSearchItem) => {
    if (item.series_id === undefined) return;
    if (item.title) titleCache.current.set(item.series_id, item.title);
    if (!value.includes(item.series_id)) onChange([...value, item.series_id]);
    setQuery('');
    setActiveIndex(-1);
    // Keep popover open + focus in input so picks can chain.
  }, [value, onChange]);

  const remove = useCallback((id: number) => {
    onChange(value.filter((v) => v !== id));
  }, [value, onChange]);

  const onInputChange = (e: ChangeEvent<HTMLInputElement>) => {
    setQuery(e.target.value);
    setOpen(true);
    setActiveIndex(-1);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setOpen(true);
      setActiveIndex((i) => Math.min(i + 1, visible.length - 1));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIndex((i) => Math.max(i - 1, 0));
      return;
    }
    if (e.key === 'Enter') {
      if (activeIndex >= 0 && visible[activeIndex]) {
        e.preventDefault();
        pick(visible[activeIndex]);
      }
      return;
    }
    if (e.key === 'Escape') {
      setOpen(false);
      setActiveIndex(-1);
      return;
    }
    if (e.key === 'Backspace' && query === '' && value.length > 0) {
      e.preventDefault();
      onChange(value.slice(0, -1));
    }
  };

  const activeId =
    activeIndex >= 0 && visible[activeIndex]?.series_id !== undefined
      ? `series-picker-opt-${visible[activeIndex]?.series_id}`
      : undefined;

  return (
    <div className={cn('flex flex-col gap-2', className)}>
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1.5" data-testid="series-picker-chips">
          {value.map((id) => {
            const label = titleCache.current.get(id) ?? `#${id}`;
            return (
              <span
                key={id}
                className="inline-flex items-center gap-1 pl-2 pr-1 h-6 rounded-md border border-border-faint bg-surface-2 font-mono text-[11.5px]"
              >
                <span className="text-foreground">{label}</span>
                <button
                  type="button"
                  className="inline-flex items-center justify-center w-4 h-4 rounded hover:bg-surface-3 disabled:opacity-50"
                  onClick={() => remove(id)}
                  disabled={disabled}
                  aria-label={t('seriesPicker.removeChipAria', { label })}
                >
                  <X className="w-3 h-3" />
                </button>
              </span>
            );
          })}
        </div>
      )}

      <div className="relative">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-faint pointer-events-none" />
          <Input
            type="text"
            value={query}
            onChange={onInputChange}
            onFocus={() => setOpen(true)}
            // Defer close so option's mousedown (which steals focus
            // briefly) registers as a pick first.
            onBlur={() => { setTimeout(() => setOpen(false), 150); }}
            onKeyDown={onKeyDown}
            placeholder={placeholder ?? t('seriesPicker.placeholder')}
            disabled={disabled || instance.length === 0}
            autoComplete="off"
            role="combobox"
            aria-expanded={open}
            aria-controls="series-picker-listbox"
            {...(activeId !== undefined ? { 'aria-activedescendant': activeId } : {})}
            className="pl-8 pr-8"
            data-testid="series-picker-input"
          />
          {isLoading && (
            <Loader2
              className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-faint animate-spin"
              aria-label={t('seriesPicker.searchingAria')}
              data-testid="series-picker-spinner"
            />
          )}
        </div>

        {open && instance.length > 0 && (
          <ul
            id="series-picker-listbox"
            role="listbox"
            data-testid="series-picker-listbox"
            className="absolute z-50 mt-1 w-full max-h-72 overflow-auto rounded-md border border-border-faint bg-surface shadow-md py-1"
          >
            {hasError && (
              <li className="px-3 py-2 text-[12px] text-status-danger">
                {t('seriesPicker.searchFailed')}
              </li>
            )}
            {!hasError && search.isPending && (
              <li className="px-3 py-2 text-[12px] text-muted">{t('seriesPicker.loading')}</li>
            )}
            {!hasError && !search.isPending && visible.length === 0 && (
              <li className="px-3 py-2 text-[12px] text-muted">
                {debouncedQuery ? t('seriesPicker.noMatch') : t('seriesPicker.typeToSearch')}
              </li>
            )}
            {!hasError && visible.map((s, idx) => {
              const id = s.series_id;
              if (id === undefined) return null;
              const isActive = idx === activeIndex;
              return (
                <li
                  key={id}
                  id={`series-picker-opt-${id}`}
                  role="option"
                  aria-selected={isActive}
                  // mousedown not click — Input's blur fires before
                  // click and our 150ms close-timer would race the
                  // click. mousedown intercepts before blur.
                  onMouseDown={(e) => { e.preventDefault(); pick(s); }}
                  onMouseEnter={() => setActiveIndex(idx)}
                  className={cn(
                    'px-3 py-1.5 cursor-pointer flex items-center gap-3 text-[13px]',
                    isActive ? 'bg-surface-2' : 'hover:bg-surface-2',
                  )}
                  data-testid={`series-picker-opt-${id}`}
                >
                  <span className="flex-1 truncate">{s.title ?? `#${id}`}</span>
                  {s.missing_aired_count !== undefined && s.missing_aired_count > 0 && (
                    <span className="font-mono text-[10.5px] text-faint shrink-0">
                      {t('seriesPicker.missingSuffix', { count: s.missing_aired_count })}
                    </span>
                  )}
                  <span className="font-mono text-[10.5px] text-faint shrink-0">#{id}</span>
                </li>
              );
            })}
            {!hasError && search.data && search.data.total !== undefined &&
              search.data.total > visible.length && (
              <li className="px-3 py-1 text-[10.5px] text-faint font-mono border-t border-border-faint">
                {t('seriesPicker.showingFooter', { shown: visible.length, total: search.data.total })}
              </li>
            )}
          </ul>
        )}
      </div>

      {helperText && <p className="text-[11px] text-muted">{helperText}</p>}
    </div>
  );
}
