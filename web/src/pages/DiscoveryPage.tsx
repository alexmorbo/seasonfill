import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { SearchBar } from '@/components/discovery/SearchBar';
import { SearchResults } from '@/components/discovery/SearchResults';
import { DiscoveryRails } from '@/components/discovery/DiscoveryRails';

// DiscoveryPage — ADR-0017 D-2: rails replace the fixed Tabs. The search bar
// stays on top and overrides the rails with SearchResults while active
// (>= 2 chars). Old tab components (TrendingGrid/PopularGrid/GenreFilter/…)
// are no longer mounted here but intentionally retained for S2 (add-row
// picker / filter builder reuses useDiscoverFilter + the pickers).
export function DiscoveryPage() {
  const { t } = useTranslation();
  useSetPageTitle(t('discovery.title'));

  const [searchQuery, setSearchQuery] = useState('');
  const isSearching = searchQuery.trim().length >= 2;

  return (
    <div className="space-y-6">
      <div data-testid="discovery-search-bar">
        <SearchBar onDebouncedChange={setSearchQuery} />
      </div>
      {isSearching ? <SearchResults q={searchQuery} /> : <DiscoveryRails />}
    </div>
  );
}
