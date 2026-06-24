import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Search, X } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface SearchBarProps {
  readonly onDebouncedChange: (value: string) => void;
  readonly delayMs?: number;
  readonly className?: string;
}

// Story 515 / N-3c: controlled search input with inline 250ms debounce.
// Mirrors the pattern in series/SeriesFiltersBar without lifting the
// hook out — discovery only consumes one debounced value.
export function SearchBar({
  onDebouncedChange, delayMs = 250, className,
}: SearchBarProps) {
  const { t } = useTranslation();
  const [value, setValue] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastEmittedRef = useRef('');

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);

  const schedule = (next: string) => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      if (lastEmittedRef.current !== next) {
        lastEmittedRef.current = next;
        onDebouncedChange(next);
      }
    }, delayMs);
  };

  const onChange = (next: string) => {
    setValue(next);
    schedule(next);
  };

  const onClear = () => {
    setValue('');
    if (timer.current) clearTimeout(timer.current);
    if (lastEmittedRef.current !== '') {
      lastEmittedRef.current = '';
      onDebouncedChange('');
    }
  };

  return (
    <div className={cn('relative w-full max-w-md', className)}>
      <Search
        aria-hidden="true"
        className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-tx-muted"
      />
      <input
        type="search"
        aria-label="search"
        data-testid="discovery-search-input"
        placeholder={t('discovery.search.placeholder')}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={cn(
          'flex h-9 w-full rounded-md border border-strong bg-input pl-9 pr-9 py-1',
          'text-base shadow-xs transition-colors placeholder:text-muted',
          'focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring md:text-sm',
        )}
      />
      {value.length > 0 ? (
        <button
          type="button" onClick={onClear} aria-label="clear search"
          data-testid="discovery-search-clear"
          className={cn(
            'absolute right-2 top-1/2 inline-flex h-6 w-6 -translate-y-1/2 items-center justify-center',
            'rounded text-tx-muted hover:text-tx-primary',
            'focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent',
          )}
        ><X className="h-4 w-4" /></button>
      ) : null}
    </div>
  );
}
