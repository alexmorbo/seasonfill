import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';

import { SeriesGrid } from './SeriesGrid';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

function item(
  id: number,
  title: string,
  extra: Partial<SeriesCacheItem> = {},
): SeriesCacheItem {
  return {
    sonarr_series_id: id,
    instance_name: 'homelab',
    series_id: id,
    title,
    title_slug: title.toLowerCase().replace(/\s+/g, '-'),
    year: 2024,
    network: 'HBO',
    status: 'continuing',
    monitored: true,
    missing_count: 0,
    updated_at: new Date().toISOString(),
    ...extra,
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

  it('renders a unified SeriesCard per item', () => {
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
    expect(screen.getAllByTestId('series-card')).toHaveLength(2);
    // the legacy tile is gone
    expect(screen.queryByTestId('series-poster-tile')).toBeNull();
  });

  it('links each card to /series/:id (canonical id)', () => {
    render(wrap(
      <SeriesGrid
        items={[item(42, 'Severance')]}
        isLoading={false}
        isFetchingNextPage={false}
        hasNextPage={false}
        onLoadMore={vi.fn()}
      />
    ));
    const card = screen.getByTestId('series-card');
    expect(card.tagName.toLowerCase()).toBe('a');
    expect(card.getAttribute('href')).toBe('/series/42');
  });

  it('renders the ★ rating from tmdb_rating', () => {
    render(wrap(
      <SeriesGrid
        items={[item(1, 'Severance', { tmdb_rating: 8.4 })]}
        isLoading={false}
        isFetchingNextPage={false}
        hasNextPage={false}
        onLoadMore={vi.fn()}
      />
    ));
    expect(screen.getByTestId('series-card-rating')).toHaveTextContent('8.4');
  });

  it('renders the missing chip from missing_count', () => {
    render(wrap(
      <SeriesGrid
        items={[item(1, 'Severance', { missing_count: 3 })]}
        isLoading={false}
        isFetchingNextPage={false}
        hasNextPage={false}
        onLoadMore={vi.fn()}
      />
    ));
    expect(screen.getByTestId('series-card-missing-chip')).toHaveTextContent('3');
  });

  it('omits the removed affordances (monitored dot, sonarr link, timestamp)', () => {
    render(wrap(
      <SeriesGrid
        items={[item(1, 'Severance', { missing_count: 2, last_grab_at: new Date().toISOString() })]}
        isLoading={false}
        isFetchingNextPage={false}
        hasNextPage={false}
        onLoadMore={vi.fn()}
      />
    ));
    const card = screen.getByTestId('series-card');
    expect(within(card).queryByTestId('sonarr-link')).toBeNull();
    expect(card.querySelector('[data-testid*="monitored"]')).toBeNull();
    expect(card.querySelector('.bg-ok')).toBeNull();
    expect(card).not.toHaveTextContent(/ago|назад/i);
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
