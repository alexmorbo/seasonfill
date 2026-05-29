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
          { name: 'alpha', health: 'Available', last_check_at: new Date().toISOString() },
          { name: 'beta', health: 'UnavailableNetwork', last_check_at: new Date().toISOString() },
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

  it('counts healthy/down against the real backend health strings (guards casing)', async () => {
    globalThis.fetch = fetchStub({
      '/instances': {
        instances: [
          { name: 'alpha', health: 'Available', last_check_at: new Date().toISOString() },
          { name: 'beta', health: 'Available', last_check_at: new Date().toISOString() },
          { name: 'gamma', health: 'UnavailableAuth', last_check_at: new Date().toISOString() },
        ],
      },
      '/scans': { items: [] },
      '/grabs': { items: [] },
    }) as typeof fetch;
    renderWithProviders(wrap(<Dashboard />));

    // 2 of 3 are 'Available'. A lowercase compare ('available') would yield 0.
    expect(await screen.findByText('2')).toBeInTheDocument();
    expect(screen.getByText('/ 3')).toBeInTheDocument();
    // Footer reflects the real counts: 2 healthy, 1 down.
    expect(screen.getByText(/2 healthy/)).toBeInTheDocument();
    expect(screen.getByText(/1 down/)).toBeInTheDocument();

    // The health table renders the localized danger label, not the raw string.
    expect(screen.getByText('Unavailable (auth)')).toBeInTheDocument();
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
