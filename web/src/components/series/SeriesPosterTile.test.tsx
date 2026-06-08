import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { SeriesPosterTile } from './SeriesPosterTile';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

if (!i18n.isInitialized) {
  void i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  });
}

function makeItem(overrides: Partial<SeriesCacheItem> = {}): SeriesCacheItem {
  return {
    sonarr_series_id: 122,
    instance_name: 'homelab',
    title: 'For All Mankind',
    title_slug: 'for-all-mankind',
    year: 2019,
    network: 'Apple TV+',
    status: 'continuing',
    poster_path: '/MediaCover/122/poster.jpg',
    monitored: true,
    missing_count: 0,
    last_grab_at: new Date(Date.now() - 60_000).toISOString(),
    updated_at: new Date(Date.now() - 30_000).toISOString(),
    ...overrides,
  } as SeriesCacheItem;
}

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="probe-location">{loc.pathname + loc.search}</div>;
}

function renderTile(item: SeriesCacheItem) {
  render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<SeriesPosterTile item={item} />} />
          <Route path="/grabs" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe('<SeriesPosterTile />', () => {
  it('renders proxy img with size=full for the instance + series id', () => {
    renderTile(makeItem());
    const img = screen.getByTestId('series-poster-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe(
      '/api/v1/instances/homelab/series/122/poster?size=full',
    );
    expect(img.getAttribute('loading')).toBe('lazy');
  });

  it('renders title and metadata footer', () => {
    renderTile(makeItem());
    expect(screen.getByText('For All Mankind')).toBeInTheDocument();
    expect(screen.getByText(/Apple TV\+/)).toBeInTheDocument();
  });

  it('renders monitored dot when monitored=true', () => {
    renderTile(makeItem({ monitored: true }));
    const tile = screen.getByTestId('series-poster-tile');
    expect(tile.getAttribute('data-monitored')).toBe('true');
  });

  it('renders monitored=false data attribute when monitored=false', () => {
    renderTile(makeItem({ monitored: false }));
    const tile = screen.getByTestId('series-poster-tile');
    expect(tile.getAttribute('data-monitored')).toBe('false');
  });

  it('hides missing chip when missing_count=0', () => {
    renderTile(makeItem({ missing_count: 0 }));
    expect(screen.queryByTestId('series-tile-missing-chip')).toBeNull();
  });

  it('shows missing chip when missing_count>0', () => {
    renderTile(makeItem({ missing_count: 7 }));
    expect(screen.getByTestId('series-tile-missing-chip')).toBeInTheDocument();
  });

  it('navigates to /grabs?series=<sonarr_series_id> on click', () => {
    renderTile(makeItem());
    fireEvent.click(screen.getByTestId('series-poster-tile'));
    expect(screen.getByTestId('probe-location').textContent).toBe(
      '/grabs?series=122',
    );
  });

  it('navigates on Enter keypress', () => {
    renderTile(makeItem());
    const tile = screen.getByTestId('series-poster-tile');
    fireEvent.keyDown(tile, { key: 'Enter' });
    expect(screen.getByTestId('probe-location').textContent).toBe(
      '/grabs?series=122',
    );
  });
});
