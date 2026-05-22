import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Dashboard } from './Dashboard';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

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
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

describe('<Dashboard />', () => {
  it('renders instances + stat cards from useInstances', async () => {
    globalThis.fetch = fetchStub({
      '/instances': {
        instances: [
          { name: 'alpha', health: 'available', last_check_at: new Date().toISOString() },
          { name: 'beta', health: 'degraded', last_check_at: new Date().toISOString() },
        ],
      },
      '/scans': { items: [] },
      '/grabs': { items: [] },
    }) as typeof fetch;
    renderWithProviders(wrap(<Dashboard />));
    expect(await screen.findByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
    expect(screen.getByText(/instances/i)).toBeInTheDocument();
  });

  it('renders empty state for recent scans when items=[]', async () => {
    globalThis.fetch = fetchStub({
      '/instances': { instances: [] },
      '/scans': { items: [] },
      '/grabs': { items: [] },
    }) as typeof fetch;
    renderWithProviders(wrap(<Dashboard />));
    await waitFor(() => expect(screen.getByText(/no scans yet/i)).toBeInTheDocument());
  });
});
