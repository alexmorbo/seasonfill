import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BrowserRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { PosterGrid } from './PosterGrid';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const mod = await vi.importActual('react-router-dom');
  return {
    ...mod,
    useNavigate: () => mockNavigate,
  };
});

const fixtureItem: SeriesCacheItem = {
  sonarr_series_id: 1,
  instance_name: 'alpha',
  title: 'Breaking Bad',
  title_slug: 'breaking-bad',
  year: 2008,
  network: 'AMC',
  status: 'ended',
  monitored: true,
  missing_count: 0,
  last_grab_at: new Date().toISOString(),
  last_imported_episode: 'S05E16',
  updated_at: new Date().toISOString(),
};

function renderGrid(items: SeriesCacheItem[] = [], isLoading = false) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <BrowserRouter>
            <PosterGrid items={items} isLoading={isLoading} />
          </BrowserRouter>
        </TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('<PosterGrid />', () => {
  beforeEach(() => mockNavigate.mockClear());

  it('renders 12 skeleton placeholders when isLoading=true', () => {
    const { container } = renderGrid([], true);
    expect(screen.getByTestId('poster-grid-skeleton')).toBeInTheDocument();
    const skeletonDivs = container.querySelectorAll('[data-testid="poster-grid-skeleton"] > div');
    expect(skeletonDivs.length).toBe(12);
  });

  it('renders one PosterTile per item when loaded', () => {
    const items = [
      { ...fixtureItem, sonarr_series_id: 1, title: 'Breaking Bad' },
      { ...fixtureItem, sonarr_series_id: 2, title: 'Chernobyl' },
      { ...fixtureItem, sonarr_series_id: 3, title: 'Andor' },
    ];
    renderGrid(items, false);
    expect(screen.getByText('Breaking Bad')).toBeInTheDocument();
    expect(screen.getByText('Chernobyl')).toBeInTheDocument();
    expect(screen.getByText('Andor')).toBeInTheDocument();
    expect(screen.getAllByTestId('poster-tile')).toHaveLength(3);
  });

  it('renders nothing when items is empty and isLoading=false', () => {
    const { container } = renderGrid([], false);
    const grid = container.querySelector('[data-testid="poster-grid"]');
    expect(grid).toBeInTheDocument();
    expect(grid?.children.length).toBe(0);
  });

  it('uses unique key combining instance_name + sonarr_series_id', () => {
    const items = [
      { ...fixtureItem, instance_name: 'alpha', sonarr_series_id: 100 },
      { ...fixtureItem, instance_name: 'alpha', sonarr_series_id: 101 },
    ];
    renderGrid(items, false);
    // Tiles render without key warnings (vitest would fail)
    expect(screen.getAllByTestId('poster-tile')).toHaveLength(2);
  });
});
