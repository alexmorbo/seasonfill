import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { Series } from './Series';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

vi.mock('@/lib/instances', () => ({
  useInstances: () => ({
    data: { instances: [{ name: 'homelab', ui_url: 'http://sonarr' }] },
    isPending: false,
  }),
}));

vi.mock('@/lib/api/seriesCache', () => ({
  useSeriesCacheInfinite: () => ({
    data: { pages: [{ items: [], total: 0, has_more: false }], pageParams: [''] },
    isPending: false,
    isFetching: false,
    isFetchingNextPage: false,
    isError: false,
    isSuccess: true,
    hasNextPage: false,
    refetch: vi.fn(),
    fetchNextPage: vi.fn(),
  }),
  flattenSeriesCachePages: (pages: { items: unknown[] }[] | undefined) =>
    pages ? pages.flatMap((p) => p.items ?? []) : [],
}));

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/series']}>
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

describe('<Series /> smoke', () => {
  beforeEach(() => {
    // Ensure the i18n instance has the keys we need; tests in 059c will
    // bind the full locale files. For this smoke we only need the keys
    // to NOT throw — i18next falls back to the key string when missing.
    if (!i18n.isInitialized) {
      void i18n.init({
        lng: 'en',
        fallbackLng: 'en',
        resources: { en: { translation: {} } },
        interpolation: { escapeValue: false },
      });
    }
  });

  it('renders the filters bar', () => {
    renderPage();
    expect(screen.getByTestId('series-filters-bar')).toBeInTheDocument();
  });

  it('renders the server-empty CTA when items=0', () => {
    renderPage();
    expect(screen.getByTestId('series-empty-server')).toBeInTheDocument();
  });
});
