import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from 'i18next';

import { SeriesGrid } from './SeriesGrid';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

if (!i18n.isInitialized) {
  void i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  });
}

function item(id: number, title: string): SeriesCacheItem {
  return {
    sonarr_series_id: id,
    instance_name: 'homelab',
    title,
    title_slug: title.toLowerCase().replace(/\s+/g, '-'),
    year: 2024,
    network: 'HBO',
    status: 'continuing',
    poster_path: `/MediaCover/${id}/poster.jpg`,
    monitored: true,
    missing_count: 0,
    updated_at: new Date().toISOString(),
  } as SeriesCacheItem;
}

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter>{ui}</MemoryRouter>
        </TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>
  );
}

describe('<SeriesGrid />', () => {
  it('renders skeleton when isLoading=true', () => {
    render(wrap(
      <SeriesGrid items={[]} isLoading isFetchingNextPage={false} hasNextPage={false} onLoadMore={vi.fn()} />
    ));
    expect(screen.getByTestId('series-grid-skeleton')).toBeInTheDocument();
  });

  it('renders tiles when items present', () => {
    render(wrap(
      <SeriesGrid
        items={[item(1, 'Severance'), item(2, 'Wednesday')]}
        isLoading={false}
        isFetchingNextPage={false}
        hasNextPage={false}
        onLoadMore={vi.fn()}
      />
    ));
    expect(screen.getByTestId('series-grid')).toBeInTheDocument();
    expect(screen.getAllByTestId('series-poster-tile')).toHaveLength(2);
  });

  it('renders the next-page skeleton when isFetchingNextPage=true', () => {
    render(wrap(
      <SeriesGrid
        items={[item(1, 'Severance')]}
        isLoading={false}
        isFetchingNextPage
        hasNextPage
        onLoadMore={vi.fn()}
      />
    ));
    expect(screen.getByTestId('series-grid-next-skeleton')).toBeInTheDocument();
  });

  it('always renders the IntersectionObserver sentinel', () => {
    render(wrap(
      <SeriesGrid
        items={[item(1, 'A')]}
        isLoading={false}
        isFetchingNextPage={false}
        hasNextPage
        onLoadMore={vi.fn()}
      />
    ));
    expect(screen.getByTestId('series-grid-sentinel')).toBeInTheDocument();
  });
});
