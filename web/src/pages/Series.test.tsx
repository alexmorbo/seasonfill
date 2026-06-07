import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { Series } from './Series';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

if (!i18n.isInitialized) {
  void i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  });
}

let instancesFixture: {
  data?: { instances: Array<{ name: string; ui_url: string }> };
  isPending: boolean;
} = { data: { instances: [{ name: 'homelab', ui_url: 'http://sonarr' }] }, isPending: false };

let infiniteFixture: {
  data?: { pages: Array<{ items: unknown[]; total: number; has_more: boolean }>; pageParams: string[] };
  isPending: boolean;
  isFetching: boolean;
  isFetchingNextPage: boolean;
  isError: boolean;
  isSuccess: boolean;
  hasNextPage: boolean;
  refetch: () => void;
  fetchNextPage: () => void;
};

const refetch = vi.fn();
const fetchNextPage = vi.fn();

function resetInfinite(overrides: Partial<typeof infiniteFixture> = {}) {
  refetch.mockReset();
  fetchNextPage.mockReset();
  infiniteFixture = {
    data: { pages: [{ items: [], total: 0, has_more: false }], pageParams: [''] },
    isPending: false,
    isFetching: false,
    isFetchingNextPage: false,
    isError: false,
    isSuccess: true,
    hasNextPage: false,
    refetch,
    fetchNextPage,
    ...overrides,
  };
}

vi.mock('@/lib/instances', () => ({
  useInstances: () => instancesFixture,
}));

vi.mock('@/lib/api/seriesCache', async () => {
  const real = await vi.importActual<typeof import('@/lib/api/seriesCache')>(
    '@/lib/api/seriesCache',
  );
  return {
    ...real,
    useSeriesCacheInfinite: () => infiniteFixture,
    flattenSeriesCachePages: (pages: Array<{ items: unknown[] }> | undefined) =>
      pages ? pages.flatMap((p) => p.items ?? []) : [],
  };
});

function renderPage(url: string = '/series') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <InstanceFilterCtx.Provider value={{ filter: null, setFilter: vi.fn() }}>
            <PageTitleProvider defaultTitle="Series">
              <Series />
            </PageTitleProvider>
          </InstanceFilterCtx.Provider>
        </MemoryRouter>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('<Series /> integration', () => {
  beforeEach(() => {
    instancesFixture = {
      data: { instances: [{ name: 'homelab', ui_url: 'http://sonarr' }] },
      isPending: false,
    };
    resetInfinite();
  });

  it('renders the filters bar after 059b lands', () => {
    renderPage();
    expect(screen.getByTestId('series-filters-bar')).toBeInTheDocument();
  });

  it('renders the server-empty state when items=0', () => {
    renderPage();
    expect(screen.getByTestId('series-empty-server')).toBeInTheDocument();
  });

  it('renders first-run when no instances configured', () => {
    instancesFixture = { data: { instances: [] }, isPending: false };
    renderPage();
    expect(screen.getByTestId('series-first-run')).toBeInTheDocument();
  });

  it('renders the grid when items present', () => {
    resetInfinite({
      data: {
        pages: [{
          items: [{
            sonarr_series_id: 1,
            instance_name: 'homelab',
            title: 'Severance',
            title_slug: 'severance',
            monitored: true,
            missing_count: 0,
            updated_at: new Date().toISOString(),
          }],
          total: 1,
          has_more: false,
        }],
        pageParams: [''],
      },
    });
    renderPage();
    expect(screen.getByTestId('series-grid')).toBeInTheDocument();
  });

  it('refetch is called when refresh clicked', () => {
    renderPage();
    fireEvent.click(screen.getByTestId('series-header-refresh'));
    expect(refetch).toHaveBeenCalledTimes(1);
  });
});
