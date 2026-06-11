import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
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

interface InfiniteFixture {
  data?: { pages: Array<{ items: unknown[]; total: number; has_more: boolean }>; pageParams: string[] };
  isPending: boolean;
  isFetching: boolean;
  isFetchingNextPage: boolean;
  isError: boolean;
  isSuccess: boolean;
  hasNextPage: boolean;
  refetch: () => void;
  fetchNextPage: () => void;
  error?: unknown;
}

let infiniteFixture: InfiniteFixture;

interface InfiniteHookCall {
  instance: string | null | undefined;
  q: {
    state: string;
    sort: string;
    limit: number;
    search?: string;
  };
}

const hookCalls: InfiniteHookCall[] = [];

const refetch = vi.fn();
const fetchNextPage = vi.fn();

function resetInfinite(overrides: Partial<InfiniteFixture> = {}) {
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
    useSeriesCacheInfinite: (
      instance: string | null | undefined,
      q: InfiniteHookCall['q'],
    ) => {
      hookCalls.push({ instance, q });
      return infiniteFixture;
    },
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

const itemFixture = {
  sonarr_series_id: 1,
  instance_name: 'homelab',
  title: 'Severance',
  title_slug: 'severance',
  monitored: true,
  missing_count: 1,
  updated_at: new Date().toISOString(),
};

describe('<Series /> integration', () => {
  beforeEach(() => {
    instancesFixture = {
      data: { instances: [{ name: 'homelab', ui_url: 'http://sonarr' }] },
      isPending: false,
    };
    hookCalls.length = 0;
    resetInfinite();
  });

  it('renders the filters bar', () => {
    renderPage();
    expect(screen.getByTestId('series-filters-bar')).toBeInTheDocument();
  });

  it('renders first-run when no instances configured', () => {
    instancesFixture = { data: { instances: [] }, isPending: false };
    renderPage();
    expect(screen.getByTestId('series-first-run')).toBeInTheDocument();
  });

  it('refetch is called when refresh clicked', () => {
    resetInfinite({
      data: { pages: [{ items: [itemFixture], total: 1, has_more: false }], pageParams: [''] },
    });
    renderPage();
    fireEvent.click(screen.getByTestId('series-header-refresh'));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it('queries the cache with state=missing on bare URL (F-P1-2 default)', () => {
    resetInfinite({
      data: { pages: [{ items: [itemFixture], total: 1, has_more: false }], pageParams: [''] },
    });
    renderPage();
    expect(hookCalls.length).toBeGreaterThanOrEqual(1);
    expect(hookCalls[0]!.q.state).toBe('missing');
  });

  it('renders the monitored switch checked on bare URL (F-P1-1 default)', () => {
    resetInfinite({
      data: { pages: [{ items: [itemFixture], total: 1, has_more: false }], pageParams: [''] },
    });
    renderPage();
    expect(screen.getByTestId('series-filters-monitored').getAttribute('aria-checked'))
      .toBe('true');
  });

  it('auto-falls-back to state=all when first load on state=missing has total=0', async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('series-fallback-hint')).toBeInTheDocument();
    });
    const lastCall = hookCalls[hookCalls.length - 1]!;
    expect(lastCall.q.state).toBe('all');
  });

  it('does NOT auto-fall-back when state=missing has results', async () => {
    resetInfinite({
      data: { pages: [{ items: [itemFixture], total: 1, has_more: false }], pageParams: [''] },
    });
    renderPage();
    await act(async () => { /* let effects flush */ });
    expect(screen.queryByTestId('series-fallback-hint')).toBeNull();
    expect(hookCalls.every((c) => c.q.state === 'missing')).toBe(true);
  });

  it('does NOT auto-fall-back on deep links that arrive with state=all', async () => {
    renderPage('/series?state=all');
    await act(async () => { /* let effects flush */ });
    expect(screen.queryByTestId('series-fallback-hint')).toBeNull();
    expect(hookCalls.every((c) => c.q.state === 'all')).toBe(true);
  });

  it('propagates sort=air_date_desc from URL to the hook query', async () => {
    hookCalls.length = 0;
    renderPage('/series?sort=air_date_desc');
    await waitFor(() => {
      expect(hookCalls.length).toBeGreaterThan(0);
    });
    expect(hookCalls[hookCalls.length - 1]!.q.sort).toBe('air_date_desc');
  });

  it('propagates ?q= from the URL into the hook search param', async () => {
    hookCalls.length = 0;
    renderPage('/series?q=rick');
    await waitFor(() => {
      expect(hookCalls.length).toBeGreaterThan(0);
    });
    expect(hookCalls[hookCalls.length - 1]!.q.search).toBe('rick');
  });

  it('typing in the search box updates the hook search param', async () => {
    resetInfinite({
      data: { pages: [{ items: [itemFixture], total: 1, has_more: false }], pageParams: [''] },
    });
    renderPage();
    hookCalls.length = 0;
    const input = screen.getByTestId('series-filters-search');
    fireEvent.change(input, { target: { value: 'severance' } });
    await waitFor(() => {
      expect(hookCalls.some((c) => c.q.search === 'severance')).toBe(true);
    });
  });

  it('renders an inline error alert when the list query fails', async () => {
    resetInfinite({
      isError: true,
      isSuccess: false,
      error: new Error('boom'),
    });
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('series-list-error')).toBeInTheDocument();
    });
    expect(screen.getByTestId('series-list-error')).toHaveTextContent(/Failed to load series|boom/i);
  });

  it('hides the grid when the list query fails', async () => {
    resetInfinite({
      isError: true,
      isSuccess: false,
      error: new Error('boom'),
    });
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('series-list-error')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('series-grid')).not.toBeInTheDocument();
  });
});
