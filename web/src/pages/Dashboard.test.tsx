import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { Dashboard } from './Dashboard';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

vi.mock('@/lib/api/webhookStatus', () => ({ useWebhookStatusAggregate: vi.fn() }));
vi.mock('@/lib/api/watchdogRollups', async () => {
  const a = await vi.importActual<typeof import('@/lib/api/watchdogRollups')>('@/lib/api/watchdogRollups');
  return { ...a, useWatchdogRollups: vi.fn() };
});
import { useWebhookStatusAggregate } from '@/lib/api/webhookStatus';
import { useWatchdogRollups } from '@/lib/api/watchdogRollups';
const useWh = vi.mocked(useWebhookStatusAggregate);
const useRoll = vi.mocked(useWatchdogRollups);
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const okR = <T,>(d: T) => ({ data: d, isPending: false, isError: false } as any);

function fetchStub(payloads: Record<string, unknown>) {
  return vi.fn(async (url: RequestInfo | URL) => {
    const path = typeof url === 'string' ? url : url.toString();
    for (const key of Object.keys(payloads)) {
      if (path.includes(key)) {
        return new Response(JSON.stringify(payloads[key]), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
    }
    return new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
}

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/', search: '', assign: vi.fn() },
  });
});

afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = (ui: ReactElement) => (
  <PageTitleProvider defaultTitle="Dashboard">
    <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
  </PageTitleProvider>
);

describe('<Dashboard /> — 049a smoke test', () => {
  beforeEach(() => { useWh.mockReset(); useRoll.mockReset(); });

  it('mounts without crash and renders first-run state when zero instances', async () => {
    globalThis.fetch = fetchStub({
      '/instances': { instances: [] },
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));
    renderWithProviders(wrap(<Dashboard />));
    expect(await screen.findByTestId('dashboard-first-run')).toBeInTheDocument();
  });

  it('rail mounts in data state with all 4 cards', async () => {
    globalThis.fetch = fetchStub({
      '/instances': { instances: [{ name: 'a', url: 'http://a', health: 'available' }] },
      '/counters': { items: [{ instance_name: 'a', window: '24h', totals: { grabs: 10, imports: 9, fails: 1 }, sparkline: [], avg_grabs_7d: 5 }] },
      '/series/cache': { items: [], has_more: false },
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));
    renderWithProviders(wrap(<Dashboard />));
    expect(await screen.findByTestId('dashboard-rail')).toBeInTheDocument();
    expect(await screen.findByTestId('today-card')).toBeInTheDocument();
    expect(screen.getByTestId('alerts-card')).toBeInTheDocument();
    expect(screen.getByTestId('quick-actions-card')).toBeInTheDocument();
    expect(screen.getByTestId('watchdog-mini-card')).toBeInTheDocument();
  });

  it('rail hidden in first-run state (zero instances)', async () => {
    globalThis.fetch = fetchStub({
      '/instances': { instances: [] },
    }) as typeof fetch;
    useWh.mockReturnValue(okR({ items: [], healthy_count: 0, unhealthy_count: 0 }));
    useRoll.mockReturnValue(okR({ items: [] }));
    renderWithProviders(wrap(<Dashboard />));
    expect(await screen.findByTestId('dashboard-first-run')).toBeInTheDocument();
    expect(screen.queryByTestId('dashboard-rail')).toBeNull();
  });
});
