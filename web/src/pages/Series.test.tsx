import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { Series, decideEmptyBranch, type EmptyBranch } from './Series';
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
    monitoredOnly?: boolean;
    networks?: readonly string[];
    lang?: string;
  };
}

// New mock for the networks-list query (story 121a §A).
const networksFixture = {
  data: ['Apple TV+', 'HBO', 'Netflix'],
  isPending: false,
  isError: false,
};

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

// Story 495 / N-1e (B-15): mock useInstanceLatestScan so the empty-state
// branching logic can be driven from the tests below.
let latestScanFixture: {
  data: { id: string; status: string } | null;
  isPending: boolean;
} = { data: null, isPending: false };

vi.mock('@/lib/scans', async () => {
  const real = await vi.importActual<typeof import('@/lib/scans')>('@/lib/scans');
  return { ...real, useInstanceLatestScan: () => latestScanFixture };
});

// Story 495 / N-1e (B-15 branch 3): SeriesFirstScanState uses
// useTriggerScan + sonner toast. Stub both so the CTA renders without
// firing real network requests.
vi.mock('@/lib/scan-mutations', async () => {
  const real = await vi.importActual<typeof import('@/lib/scan-mutations')>('@/lib/scan-mutations');
  return {
    ...real,
    useTriggerScan: () => ({
      mutateAsync: vi.fn().mockResolvedValue([{ scan_run_id: 'sr-fake' }]),
      isPending: false,
    }),
  };
});

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() },
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
    useSeriesCacheNetworks: () => networksFixture,
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

  it('typing in the search box updates the hook search param (debounced 250ms)', () => {
    vi.useFakeTimers();
    try {
      resetInfinite({
        data: { pages: [{ items: [itemFixture], total: 1, has_more: false }], pageParams: [''] },
      });
      renderPage();
      hookCalls.length = 0;
      const input = screen.getByTestId('series-filters-search');
      fireEvent.change(input, { target: { value: 'severance' } });
      // Parent's onChange (URL update) only fires after 250ms.
      expect(hookCalls.some((c) => c.q.search === 'severance')).toBe(false);
      act(() => {
        vi.advanceTimersByTime(250);
      });
      expect(hookCalls.some((c) => c.q.search === 'severance')).toBe(true);
    } finally {
      vi.useRealTimers();
    }
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

  it('passes monitoredOnly=true into the hook when the default toggle is on', async () => {
    hookCalls.length = 0;
    renderPage('/series');
    await waitFor(() => expect(hookCalls.length).toBeGreaterThan(0));
    expect(hookCalls[hookCalls.length - 1]!.q.monitoredOnly).toBe(true);
  });

  it('omits monitoredOnly when the URL says monitored=0', async () => {
    hookCalls.length = 0;
    renderPage('/series?monitored=0');
    await waitFor(() => expect(hookCalls.length).toBeGreaterThan(0));
    expect(hookCalls[hookCalls.length - 1]!.q.monitoredOnly).toBeUndefined();
  });

  it('threads the active UI language into the infinite hook query (C-grid-lang)', async () => {
    hookCalls.length = 0;
    renderPage('/series');
    await waitFor(() => expect(hookCalls.length).toBeGreaterThan(0));
    const lang = hookCalls[hookCalls.length - 1]!.q.lang;
    expect(typeof lang).toBe('string');
    expect(lang!.length).toBeGreaterThan(0);
  });

  it('passes the networks set into the hook', async () => {
    hookCalls.length = 0;
    renderPage('/series?networks=HBO|Netflix');
    await waitFor(() => expect(hookCalls.length).toBeGreaterThan(0));
    expect(hookCalls[hookCalls.length - 1]!.q.networks).toEqual(['HBO', 'Netflix']);
  });

  it('renders the full distinct network list in the facet panel', async () => {
    const user = userEvent.setup();
    renderPage('/series');
    // The mocked useSeriesCacheNetworks returns three; the panel must
    // surface all three regardless of which page is loaded.
    await waitFor(() => {
      expect(screen.getByTestId('series-filters-networks')).toBeInTheDocument();
    });
    // Open the Radix DropdownMenu — userEvent emits the pointer events
    // Radix needs; fireEvent.click alone does not open the portal.
    await user.click(screen.getByTestId('series-filters-networks'));
    expect(await screen.findByTestId('series-filters-networks-item-Apple TV+')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-networks-item-HBO')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-networks-item-Netflix')).toBeInTheDocument();
  });
});

describe('B-15 empty-state branching', () => {
  beforeEach(() => {
    instancesFixture = {
      data: { instances: [{ name: 'homelab', ui_url: 'http://sonarr' }] },
      isPending: false,
    };
    hookCalls.length = 0;
    resetInfinite();
    latestScanFixture = { data: null, isPending: false };
  });

  it('renders firstRun branch when no instances configured', () => {
    instancesFixture = { data: { instances: [] }, isPending: false };
    renderPage();
    expect(screen.getByTestId('series-first-run')).toBeInTheDocument();
    expect(screen.queryByTestId('series-empty-scan-running')).toBeNull();
    expect(screen.queryByTestId('series-empty-first-scan')).toBeNull();
    expect(screen.queryByTestId('series-empty-all-healthy')).toBeNull();
  });

  it('renders scanRunning branch when latest scan is running', async () => {
    latestScanFixture = {
      data: { id: 'sr-123', status: 'running' },
      isPending: false,
    };
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('series-empty-scan-running')).toBeInTheDocument();
    });
    const link = screen.getByTestId('series-empty-scan-link');
    expect(link.getAttribute('href')).toBe('/scans/sr-123');
    expect(screen.queryByTestId('series-empty-first-scan')).toBeNull();
    expect(screen.queryByTestId('series-empty-all-healthy')).toBeNull();
  });

  it('renders firstScan branch when no scan has run yet', async () => {
    latestScanFixture = { data: null, isPending: false };
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('series-empty-first-scan')).toBeInTheDocument();
    });
    const cta = screen.getByTestId('series-empty-first-scan-cta') as HTMLButtonElement;
    expect(cta.disabled).toBe(false);
    expect(screen.queryByTestId('series-empty-scan-running')).toBeNull();
    expect(screen.queryByTestId('series-empty-all-healthy')).toBeNull();
  });

  it('renders allHealthy branch when latest scan completed with zero results', async () => {
    latestScanFixture = {
      data: { id: 'sr-999', status: 'completed' },
      isPending: false,
    };
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('series-empty-all-healthy')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('series-empty-scan-running')).toBeNull();
    expect(screen.queryByTestId('series-empty-first-scan')).toBeNull();
  });
});

describe('decideEmptyBranch', () => {
  type Args = Parameters<typeof decideEmptyBranch>[0];
  const base: Args = {
    instancesPending: false,
    instanceCount: 1,
    listSuccess: true,
    rawCount: 0,
    filteredCount: 0,
    total: 0,
    latestScanStatus: undefined,
    latestScanResolved: true,
  };

  const cases: Array<{ name: string; over: Partial<Args>; want: EmptyBranch }> = [
    { name: 'no instances ⇒ firstRun', over: { instanceCount: 0 }, want: 'firstRun' },
    {
      name: 'no instances takes priority over running scan',
      over: { instanceCount: 0, latestScanStatus: 'running' },
      want: 'firstRun',
    },
    {
      name: 'list not yet resolved ⇒ null (loading)',
      over: { listSuccess: false },
      want: null,
    },
    {
      name: 'scan running ⇒ scanRunning',
      over: { latestScanStatus: 'running' },
      want: 'scanRunning',
    },
    {
      name: 'never scanned + empty cache ⇒ firstScan',
      over: { latestScanResolved: true, latestScanStatus: undefined, rawCount: 0 },
      want: 'firstScan',
    },
    {
      name: 'completed + empty cache ⇒ allHealthy',
      over: { latestScanStatus: 'completed', rawCount: 0, total: 0 },
      want: 'allHealthy',
    },
    {
      name: 'cache has rows but filters eliminate all ⇒ filtered',
      over: { rawCount: 5, filteredCount: 0, total: 5 },
      want: 'filtered',
    },
    {
      name: 'cache has rows AND filtered shows them ⇒ null (normal grid)',
      over: { rawCount: 5, filteredCount: 5, total: 5, latestScanStatus: 'completed' },
      want: null,
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(decideEmptyBranch({ ...base, ...c.over })).toBe(c.want);
    });
  }
});
