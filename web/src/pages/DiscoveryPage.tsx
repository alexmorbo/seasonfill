import { useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  Flame, TrendingUp, Tag, SlidersHorizontal,
  type LucideIcon,
} from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { EmptyState } from '@/components/EmptyState';
import { TrendingGrid } from '@/components/discovery/TrendingGrid';
import { PopularGrid } from '@/components/discovery/PopularGrid';
import { GenreFilter } from '@/components/discovery/GenreFilter';
import { GenreResultsGrid } from '@/components/discovery/GenreResultsGrid';
import { SearchBar } from '@/components/discovery/SearchBar';
import { SearchResults } from '@/components/discovery/SearchResults';
import { cn } from '@/lib/utils';

// Tab key constants — drive url <-> tab sync. Story 516 (filtered)
// swaps the placeholder TabsContent body for that tab.
const TAB_KEYS = ['trending', 'popular', 'genres', 'filtered'] as const;
export type DiscoveryTabKey = (typeof TAB_KEYS)[number];

const TAB_META: ReadonlyArray<{ key: DiscoveryTabKey; icon: LucideIcon }> = [
  { key: 'trending', icon: Flame },
  { key: 'popular',  icon: TrendingUp },
  { key: 'genres',   icon: Tag },
  { key: 'filtered', icon: SlidersHorizontal },
];

const isTabKey = (v: string | null): v is DiscoveryTabKey =>
  v !== null && (TAB_KEYS as readonly string[]).includes(v);

const TRIGGER = cn(
  'inline-flex items-center gap-2 px-[15px] py-[10px] text-[13.5px] font-medium',
  'text-tx-muted hover:text-tx-primary border-b-2 border-transparent -mb-px',
  'rounded-none bg-transparent',
  'data-[state=active]:text-tx-primary data-[state=active]:font-semibold',
  'data-[state=active]:border-accent data-[state=active]:bg-transparent',
  'data-[state=active]:shadow-none [&[data-state=active]>svg]:text-accent',
);

export function DiscoveryPage() {
  const { t } = useTranslation();
  useSetPageTitle(t('discovery.title'));

  const [params, setParams] = useSearchParams();
  const active = useMemo<DiscoveryTabKey>(() => {
    const raw = params.get('tab');
    return isTabKey(raw) ? raw : 'trending';
  }, [params]);

  const onChange = useCallback((next: string) => {
    if (!isTabKey(next)) return;
    const np = new URLSearchParams(params);
    np.set('tab', next);
    setParams(np, { replace: true });
  }, [params, setParams]);

  // Story 515 / N-3c: search query overrides the tab content when set.
  // We keep the tabs mounted underneath so re-clearing the query shows
  // the previously active tab without a refetch.
  const [searchQuery, setSearchQuery] = useState('');
  const isSearching = searchQuery.trim().length >= 2;

  // Story 515 / N-3c: genres tab carries a chip selection. State lives
  // on the page so switching tabs and back doesn't drop the choice.
  const [selectedGenreId, setSelectedGenreId] = useState<number | null>(null);

  return (
    <div className="space-y-6">
      <div data-testid="discovery-search-bar">
        <SearchBar onDebouncedChange={setSearchQuery} />
      </div>

      {isSearching ? (
        <SearchResults q={searchQuery} />
      ) : (
        <Tabs value={active} onValueChange={onChange} className="w-full">
          <TabsList
            className={cn(
              'flex gap-0.5 border-b border-border-faint bg-transparent p-0 h-auto',
              'rounded-none',
            )}
            data-testid="discovery-tabs"
          >
            {TAB_META.map(({ key, icon: Icon }) => (
              <TabsTrigger key={key} value={key} className={TRIGGER} data-tab={key}>
                <Icon className="h-4 w-4" />
                {t(`discovery.tabs.${key}`)}
              </TabsTrigger>
            ))}
          </TabsList>

          <TabsContent value="trending" className="mt-6">
            <TrendingGrid />
          </TabsContent>
          <TabsContent value="popular" className="mt-6">
            <PopularGrid />
          </TabsContent>
          <TabsContent value="genres" className="mt-6">
            <GenreFilter
              selectedGenreId={selectedGenreId}
              onSelect={setSelectedGenreId}
            />
            {selectedGenreId !== null ? (
              <GenreResultsGrid genreId={selectedGenreId} />
            ) : null}
          </TabsContent>
          <TabsContent value="filtered" className="mt-6">
            <EmptyState title={t('discovery.tabs.filtered')} />
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}
