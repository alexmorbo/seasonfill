import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { Dashboard } from './Dashboard';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

vi.mock('@/lib/api/webhookStatus', () => ({ useWebhookStatusAggregate: vi.fn() }));
vi.mock('@/lib/api/watchdogRollups', async () => {
  const a = await vi.importActual<typeof import('@/lib/api/watchdogRollups')>('@/lib/api/watchdogRollups');
  return { ...a, useWatchdogRollups: vi.fn() };
});
vi.mock('@/components/dashboard/useStepperState', () => ({
  useStepperState: vi.fn(),
}));
import { useWebhookStatusAggregate } from '@/lib/api/webhookStatus';
import { useWatchdogRollups } from '@/lib/api/watchdogRollups';
import { useStepperState } from '@/components/dashboard/useStepperState';

const useWh = vi.mocked(useWebhookStatusAggregate);
const useRoll = vi.mocked(useWatchdogRollups);
const useStepper = vi.mocked(useStepperState);
// Story 494: by default tests run with onboarding complete so the normal
// Dashboard layout renders. The first-run-state + onboarding-shell tests
// override this to `allRequiredDone: false`.
const allDone = () => ({
  steps: [],
  allRequiredDone: true,
  isLoading: false,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const okR = <T,>(d: T) => ({ data: d, isPending: false, isError: false } as any);

type Captured = {
  url?: string; body?: string;
  urls?: string[]; methods?: string[]; bodies?: string[];
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function fetchStub(
  perPath: Record<string, (init?: RequestInit) => Response>,
  captured: Captured = {},
) {
  captured.urls ??= []; captured.methods ??= []; captured.bodies ??= [];
  return vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof url === 'string' ? url : url.toString();
    const body = typeof init?.body === 'string' ? init.body : undefined;
    captured.url = path;
    if (body !== undefined) captured.body = body;
    captured.urls!.push(path);
    captured.methods!.push(init?.method ?? 'GET');
    captured.bodies!.push(body ?? '');
    for (const key of Object.keys(perPath)) {
      if (path.includes(key)) return perPath[key]!(init);
    }
    return json({});
  });
}

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

const wrap = (ui: ReactElement) => (
  <PageTitleProvider defaultTitle="Dashboard">
    <InstanceFilterCtx.Provider value={ctxValue}>
      <Routes>
        <Route path="/" element={ui} />
        <Route path="/grabs" element={<div>grabs-stub</div>} />
        <Route path="/scans" element={<div>scans-stub</div>} />
        <Route path="/instances" element={<div>instances-stub</div>} />
        <Route path="/instances/:name/queue" element={<div>queue-stub</div>} />
        <Route path="/settings" element={<div>settings-stub</div>} />
      </Routes>
    </InstanceFilterCtx.Provider>
  </PageTitleProvider>
);

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/', search: '', assign: vi.fn() },
  });
  useWh.mockReset();
  useRoll.mockReset();
  useStepper.mockReset();
  vi.clearAllMocks();
  // Default: onboarding complete → normal Dashboard layout renders. Tests
  // for first-run / onboarding shell override this.
  useStepper.mockReturnValue(allDone());
});

afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<Dashboard />', () => {
  it('renders first-run state when no instances', async () => {
    globalThis.fetch = fetchStub({
      '/instances': () => json({ instances: [] }),
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));
    useStepper.mockReturnValue({ steps: [], allRequiredDone: false, isLoading: false });

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(
      await screen.findByTestId('dashboard-first-run'),
    ).toBeInTheDocument();
    expect(screen.getByText(/add your first sonarr instance/i)).toBeInTheDocument();
    expect(screen.getByTestId('first-run-cta-add')).toBeInTheDocument();
    expect(screen.getByTestId('first-run-cta-help')).toBeInTheDocument();
    expect(screen.queryByTestId('dashboard-rail')).toBeNull();
  });

  it('Story 494: renders onboarding shell when instances exist but required steps not done', async () => {
    globalThis.fetch = fetchStub({
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'manual', health: 'Available' }] }),
      '/series-cache': () => json({ items: [], total: 0, has_more: false }),
      '/counters': () =>
        json({
          items: [
            {
              instance_name: 'alpha',
              window: '24h',
              totals: { grabs: 0, imports: 0, fails: 0 },
              avg_grabs_7d: 0,
            },
          ],
        }),
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));
    useStepper.mockReturnValue({ steps: [], allRequiredDone: false, isLoading: false });

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(
      await screen.findByTestId('dashboard-onboarding-shell'),
    ).toBeInTheDocument();
    expect(screen.getByTestId('dashboard-first-run')).toBeInTheDocument();
    expect(screen.queryByTestId('hero-greeting')).toBeNull();
  });

  it('renders hero greeting on normal load', async () => {
    globalThis.fetch = fetchStub({
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] }),
      '/series-cache': () =>
        json({
          items: [],
          total: 0,
          has_more: false,
        }),
      '/counters': () =>
        json({
          items: [
            {
              instance_name: 'alpha',
              window: '24h',
              totals: { grabs: 10, imports: 5, fails: 0 },
              avg_grabs_7d: 8,
            },
          ],
        }),
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(await screen.findByTestId('hero-greeting')).toBeInTheDocument();
  });

  it('renders empty state when zero imports in 24h and zero grabs total', async () => {
    globalThis.fetch = fetchStub({
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] }),
      '/series-cache': () =>
        json({
          items: [],
          total: 0,
          has_more: false,
        }),
      '/counters': () =>
        json({
          items: [
            {
              instance_name: 'alpha',
              window: '24h',
              totals: { grabs: 0, imports: 0, fails: 0 },
              avg_grabs_7d: 5,
            },
          ],
        }),
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(
      await screen.findByTestId('hero-greeting'),
    ).toBeInTheDocument();
  });

  it('renders hero when quiet day with last import present', async () => {
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const path = typeof url === 'string' ? url : url.toString();
      if (path.includes('/instances')) {
        return json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] });
      }
      if (path.includes('/series-cache')) {
        return json({ items: [], total: 0, has_more: false });
      }
      if (path.includes('/counters')) {
        return json({
          items: [
            {
              instance_name: 'alpha',
              window: '24h',
              totals: { grabs: 0, imports: 0, fails: 0 },
              avg_grabs_7d: 5,
            },
          ],
        });
      }
      return json({});
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(await screen.findByTestId('hero-greeting')).toBeInTheDocument();
  });


  it('renders error state when series-cache fetch fails', async () => {
    globalThis.fetch = fetchStub({
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] }),
      '/series-cache': () => json({ error: 'fetch failed' }, 500),
      '/counters': () =>
        json({
          items: [
            {
              instance_name: 'alpha',
              window: '24h',
              totals: { grabs: 10, imports: 5, fails: 0 },
              avg_grabs_7d: 8,
            },
          ],
        }),
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(
      await screen.findByTestId('recent-meta'),
    ).toBeInTheDocument();
    expect(screen.getByTestId('hero-greeting')).toBeInTheDocument();
  });

  it('renders skeleton meta while series-cache is pending', async () => {
    let resolveSeriesCache: () => void;
    const seriesCachePromise = new Promise<void>((res) => {
      resolveSeriesCache = res;
    });

    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const path = typeof url === 'string' ? url : url.toString();
      if (path.includes('/instances')) {
        return json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] });
      }
      if (path.includes('/series-cache')) {
        await seriesCachePromise;
      }
      if (path.includes('/counters')) {
        return json({
          items: [
            {
              instance_name: 'alpha',
              window: '24h',
              totals: { grabs: 10, imports: 5, fails: 0 },
              avg_grabs_7d: 8,
            },
          ],
        });
      }
      return json({});
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(
      await screen.findByTestId('recent-meta'),
    ).toBeInTheDocument();
    resolveSeriesCache!();
    await waitFor(() => {
      expect(screen.getByTestId('hero-greeting')).toBeInTheDocument();
    });
  });

  it('renders grid skeleton while series-cache is pending', async () => {
    let resolveSeriesCache: () => void;
    const seriesCachePromise = new Promise<void>((res) => {
      resolveSeriesCache = res;
    });

    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const path = typeof url === 'string' ? url : url.toString();
      if (path.includes('/instances')) {
        return json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] });
      }
      if (path.includes('/series-cache')) {
        await seriesCachePromise;
      }
      if (path.includes('/counters')) {
        return json({
          items: [
            {
              instance_name: 'alpha',
              window: '24h',
              totals: { grabs: 10, imports: 5, fails: 0 },
              avg_grabs_7d: 8,
            },
          ],
        });
      }
      return json({});
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    const gridSkeleton = await screen.findByTestId('poster-grid-skeleton');
    expect(gridSkeleton).toBeInTheDocument();
    resolveSeriesCache!();
    await waitFor(() => {
      expect(screen.queryByTestId('poster-grid-skeleton')).not.toBeInTheDocument();
    });
  });


  it('rail mounts in data state with all 4 cards (049b)', async () => {
    globalThis.fetch = fetchStub({
      '/instances': () =>
        json({ instances: [{ name: 'a', url: 'http://a', health: 'available' }] }),
      '/counters': () =>
        json({
          items: [
            {
              instance_name: 'a',
              window: '24h',
              totals: { grabs: 10, imports: 9, fails: 1 },
              sparkline: [],
              avg_grabs_7d: 5,
            },
          ],
        }),
      '/series-cache': () => json({ items: [], has_more: false }),
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));

    renderWithProviders(wrap(<Dashboard />), { route: '/' });
    expect(await screen.findByTestId('dashboard-rail')).toBeInTheDocument();
    expect(await screen.findByTestId('today-card')).toBeInTheDocument();
    expect(screen.getByTestId('alerts-card')).toBeInTheDocument();
    expect(screen.getByTestId('quick-actions-card')).toBeInTheDocument();
    expect(screen.getByTestId('watchdog-mini-card')).toBeInTheDocument();
  });
});
