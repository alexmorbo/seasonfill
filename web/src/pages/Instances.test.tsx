import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Instances } from './Instances';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/instances', search: '', assign: vi.fn() },
  });
});

afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

describe('<Instances />', () => {
  it('renders one card per instance', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          instances: [
            {
              name: 'alpha',
              health: 'available',
              last_check_at: new Date().toISOString(),
              transitions_count: 0,
            },
            {
              name: 'beta',
              health: 'degraded',
              last_check_at: new Date().toISOString(),
              transitions_count: 3,
              last_error: 'connection refused',
            },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as typeof fetch;

    renderWithProviders(wrap(<Instances />));
    expect(await screen.findByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
    expect(screen.getByText(/connection refused/i)).toBeInTheDocument();
  });

  it('renders empty state when instances=[]', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ instances: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;

    renderWithProviders(wrap(<Instances />));
    expect(await screen.findByText(/no instances configured/i)).toBeInTheDocument();
  });
});
