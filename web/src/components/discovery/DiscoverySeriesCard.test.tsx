import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { renderWithProviders } from '@/test-utils';
import { DiscoverySeriesCard } from './DiscoverySeriesCard';
import type { DiscoverySeriesItem } from '@/api/discovery';

const baseItem: DiscoverySeriesItem = {
  series_id: 42, tmdb_id: 1399, title: 'Rick and Morty', year: 2013,
  poster_path: '/abc.jpg', in_library_instances: [],
};

const renderCard = (item: DiscoverySeriesItem) =>
  renderWithProviders(
    <I18nextProvider i18n={i18n}><DiscoverySeriesCard item={item} /></I18nextProvider>,
  );

describe('<DiscoverySeriesCard />', () => {
  it('renders title, year and TMDB poster URL', () => {
    renderCard(baseItem);
    expect(screen.getByText('Rick and Morty')).toBeInTheDocument();
    expect(screen.getByTestId('discovery-card-year').textContent).toBe('2013');
    const img = screen.getByTestId('discovery-poster-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('https://image.tmdb.org/t/p/w342/abc.jpg');
    expect(img.getAttribute('loading')).toBe('lazy');
  });

  it('renders fallback when poster_path is omitted', () => {
    const { poster_path: _p, ...noPoster } = baseItem;
    void _p;
    renderCard(noPoster as DiscoverySeriesItem);
    expect(screen.queryByTestId('discovery-poster-img')).toBeNull();
    expect(screen.getByTestId('discovery-poster-fallback')).toBeInTheDocument();
  });

  it('links to /series/:series_id and conditionally shows year + badge', () => {
    renderCard(baseItem);
    const link = screen.getByTestId('discovery-series-card') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/series/42');
    expect(screen.queryByTestId('discovery-in-library-badge')).toBeNull();
  });

  it('omits year line and shows InLibraryBadge when applicable', () => {
    const { year: _y, ...noYear } = baseItem;
    void _y;
    renderCard({ ...(noYear as DiscoverySeriesItem),
      in_library_instances: ['sonarr-alpha'] });
    expect(screen.queryByTestId('discovery-card-year')).toBeNull();
    expect(screen.getByTestId('discovery-in-library-badge')).toBeInTheDocument();
  });
});
