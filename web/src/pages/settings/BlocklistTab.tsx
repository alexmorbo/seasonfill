import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, EyeOff, Loader2, Search, Tag, X } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { MediaImage } from '@/components/MediaImage';
import { cn } from '@/lib/utils';
import {
  useBlocklist, useDeleteBlocklist, useAddBlocklist, useKeywordSearch,
  type BlocklistTmdbRow, type BlocklistKeywordRow,
} from '@/api/blocklist';

// KeywordTypeahead — debounced (250ms, inline setTimeout, mirrors SearchBar)
// keyword lookup. Picking a suggestion POSTs it to the blocklist.
function KeywordTypeahead() {
  const { t } = useTranslation();
  const add = useAddBlocklist();
  const [value, setValue] = useState('');
  const [debounced, setDebounced] = useState('');
  const [open, setOpen] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);

  const onChange = (next: string) => {
    setValue(next);
    setOpen(true);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setDebounced(next), 250);
  };

  const search = useKeywordSearch(debounced, open);
  const suggestions = search.data ?? [];

  const pick = (id: number, name: string) => {
    add.mutate({ kind: 'keyword', ref_id: id, label: name });
    setValue('');
    setDebounced('');
    setOpen(false);
  };

  return (
    <div className="relative max-w-md" data-testid="keyword-typeahead">
      <Search
        aria-hidden="true"
        className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-tx-muted"
      />
      <input
        type="search"
        aria-label={t('settings.blocklist.keyword.placeholder')}
        data-testid="keyword-search-input"
        placeholder={t('settings.blocklist.keyword.placeholder')}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => setOpen(true)}
        className={cn(
          'flex h-9 w-full rounded-md border border-strong bg-input pl-9 pr-9 py-1',
          'text-base shadow-xs transition-colors placeholder:text-muted',
          'focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring md:text-sm',
        )}
      />
      {search.isFetching && (
        <Loader2 className="absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 animate-spin text-tx-muted" />
      )}
      {open && debounced.trim().length >= 2 && suggestions.length > 0 && (
        <ul
          data-testid="keyword-suggestions"
          className="absolute z-20 mt-1 w-full overflow-hidden rounded-md border border-border-faint bg-popover shadow-md"
        >
          {suggestions.map((s) => (
            <li key={s.id}>
              <button
                type="button"
                data-testid={`keyword-suggestion-${s.id}`}
                onClick={() => pick(s.id, s.name)}
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-[13px] hover:bg-accent hover:text-accent-foreground"
              >
                <Tag className="h-3.5 w-3.5 text-tx-muted" aria-hidden="true" />
                {s.name}
              </button>
            </li>
          ))}
        </ul>
      )}
      {open && debounced.trim().length >= 2 && !search.isFetching && suggestions.length === 0 && (
        <p
          data-testid="keyword-no-results"
          className="absolute z-20 mt-1 w-full rounded-md border border-border-faint bg-popover px-3 py-2 text-[13px] text-tx-faint shadow-md"
        >
          {t('settings.blocklist.keyword.noResults')}
        </p>
      )}
    </div>
  );
}

function TmdbRow({ row, onRemove }: {
  readonly row: BlocklistTmdbRow;
  readonly onRemove: (id: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <li
      data-testid={`blocklist-tmdb-${row.id}`}
      className="flex items-center gap-3 rounded-md border border-border-faint bg-bg-surface p-2.5"
    >
      <div className="relative h-14 w-10 shrink-0 overflow-hidden rounded">
        <MediaImage
          hash={row.poster_hash ?? null}
          kind="series_poster"
          title={row.title}
          fallback="monogram"
          className="absolute inset-0"
        />
      </div>
      <span className="min-w-0 flex-1 truncate text-[13.5px] font-medium" title={row.title}>
        {row.title}
      </span>
      <Button
        type="button" variant="ghost" size="sm"
        onClick={() => onRemove(row.id)}
        data-testid={`blocklist-remove-${row.id}`}
        aria-label={t('settings.blocklist.remove')}
      >
        <X className="h-3.5 w-3.5 text-status-danger" />
      </Button>
    </li>
  );
}

function KeywordRow({ row, onRemove }: {
  readonly row: BlocklistKeywordRow;
  readonly onRemove: (id: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <li
      data-testid={`blocklist-keyword-${row.id}`}
      className="flex items-center gap-2 rounded-md border border-border-faint bg-bg-surface px-3 py-2"
    >
      <Tag className="h-3.5 w-3.5 shrink-0 text-tx-muted" aria-hidden="true" />
      <span className="min-w-0 flex-1 truncate text-[13.5px]" title={row.label}>
        {row.label}
      </span>
      <Button
        type="button" variant="ghost" size="sm"
        onClick={() => onRemove(row.id)}
        data-testid={`blocklist-remove-${row.id}`}
        aria-label={t('settings.blocklist.remove')}
      >
        <X className="h-3.5 w-3.5 text-status-danger" />
      </Button>
    </li>
  );
}

// BlocklistTab — the /settings/system/blocklist section. Two lists (tmdb-hidden
// series + keyword-blocked) with optimistic removal, plus a keyword typeahead.
// Section house-style copied from AgentsTab (header + subtitle + list rows).
export function BlocklistTab() {
  const { t } = useTranslation();
  const q = useBlocklist();
  const del = useDeleteBlocklist();

  const { tmdbRows, keywordRows } = useMemo(() => {
    const rows = q.data ?? [];
    return {
      tmdbRows: rows.filter((r): r is BlocklistTmdbRow => r.kind === 'tmdb'),
      keywordRows: rows.filter((r): r is BlocklistKeywordRow => r.kind === 'keyword'),
    };
  }, [q.data]);

  const onRemove = (id: number) => del.mutate(id);

  return (
    <div className="flex flex-col gap-6 max-w-[760px]" data-testid="settings-blocklist">
      <header>
        <h1 className="text-[18px] font-[650] tracking-[-0.01em] m-0">
          {t('settings.blocklist.title')}
        </h1>
        <p className="text-[13px] text-muted">{t('settings.blocklist.subtitle')}</p>
      </header>

      {q.isLoading && (
        <div className="flex flex-col gap-3" data-testid="blocklist-loading">
          <Skeleton className="h-[72px] w-full rounded-md" />
          <Skeleton className="h-[72px] w-full rounded-md" />
        </div>
      )}

      {q.isError && (
        <Alert variant="destructive" data-testid="blocklist-load-err">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('settings.blocklist.loadErr')}</AlertTitle>
          <AlertDescription>{q.error.message}</AlertDescription>
        </Alert>
      )}

      {!q.isLoading && !q.isError && (
        <>
          <section className="flex flex-col gap-3" data-testid="blocklist-series-section">
            <h2 className="flex items-center gap-2 text-[14px] font-[650]">
              <EyeOff className="h-4 w-4 text-tx-muted" aria-hidden="true" />
              {t('settings.blocklist.seriesTitle')}
            </h2>
            {tmdbRows.length === 0 ? (
              <p className="text-[13px] text-tx-faint" data-testid="blocklist-series-empty">
                {t('settings.blocklist.seriesEmpty')}
              </p>
            ) : (
              <ul className="flex flex-col gap-2">
                {tmdbRows.map((r) => (
                  <TmdbRow key={r.id} row={r} onRemove={onRemove} />
                ))}
              </ul>
            )}
          </section>

          <section className="flex flex-col gap-3" data-testid="blocklist-keyword-section">
            <h2 className="flex items-center gap-2 text-[14px] font-[650]">
              <Tag className="h-4 w-4 text-tx-muted" aria-hidden="true" />
              {t('settings.blocklist.keywordsTitle')}
            </h2>
            <KeywordTypeahead />
            {keywordRows.length === 0 ? (
              <p className="text-[13px] text-tx-faint" data-testid="blocklist-keyword-empty">
                {t('settings.blocklist.keywordsEmpty')}
              </p>
            ) : (
              <ul className="flex flex-wrap gap-2">
                {keywordRows.map((r) => (
                  <KeywordRow key={r.id} row={r} onRemove={onRemove} />
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </div>
  );
}
