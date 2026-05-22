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
  it('renders one card per instance with mode chip + queue link', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          instances: [
            {
              name: 'alpha',
              mode: 'manual',
              health: 'available',
              last_check_at: new Date().toISOString(),
              transitions_count: 0,
            },
            {
              name: 'beta',
              mode: 'auto',
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

    // Mode chips
    expect(screen.getByTestId('mode-alpha')).toHaveTextContent('manual');
    expect(screen.getByTestId('mode-beta')).toHaveTextContent('auto');

    // Queue link wording differs by mode but both targets exist
    const alphaLink = screen.getByRole('link', { name: /open queue for alpha/i });
    expect(alphaLink).toHaveAttribute('href', '/instances/alpha/queue');
    expect(alphaLink).toHaveTextContent(/open queue/i);

    const betaLink = screen.getByRole('link', { name: /open queue for beta/i });
    expect(betaLink).toHaveAttribute('href', '/instances/beta/queue');
    expect(betaLink).toHaveTextContent(/view queue/i);
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
