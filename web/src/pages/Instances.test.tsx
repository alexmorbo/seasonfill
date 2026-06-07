import { type ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Instances } from './Instances';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/instances')) {
      return new Response(JSON.stringify({
        instances: [
          { name: 'homelab', mode: 'auto', health: 'Available', last_check_at: new Date().toISOString(), transitions_count: 0, url: 'http://sonarr:80' },
          { name: '4k', mode: 'manual', health: 'Unreachable', last_check_at: new Date().toISOString(), transitions_count: 3, url: 'http://sonarr-4k:80', last_error: 'dial tcp — connection refused' },
        ],
      }), { status: 200 });
    }
    if (url.includes('/counters')) {
      return new Response(JSON.stringify({
        instance_name: 'x', window: url.includes('24h') ? '24h' : '7d',
        totals: { grabs: 0, imports: 0, fails: 0 }, sparkline: [], avg_grabs_7d: 0,
      }), { status: 200 });
    }
    if (url.endsWith('/missing')) {
      return new Response(JSON.stringify({ items: [] }), { status: 200 });
    }
    if (url.endsWith('/webhook/status')) {
      return new Response(JSON.stringify({ installed: true }), { status: 200 });
    }
    if (url.endsWith('/qbit/settings')) {
      return new Response(JSON.stringify({ enabled: false }), { status: 200 });
    }
    return new Response('{}', { status: 200 });
  }) as never;
});

afterEach(() => { globalThis.fetch = origFetch; });

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

describe('<Instances />', () => {
  it('renders hero + 1 compact row + ghost row with two instances', async () => {
    renderWithProviders(wrap(<Instances />));
    await waitFor(() => {
      expect(screen.getByTestId('instance-hero-homelab')).toBeInTheDocument();
    });
    expect(screen.getByTestId('instance-row-4k')).toBeInTheDocument();
    expect(screen.getByTestId('instance-add-ghost')).toBeInTheDocument();
  });

  it('shows empty state when zero instances', async () => {
    globalThis.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ instances: [] }), { status: 200 }),
    ) as never;
    renderWithProviders(wrap(<Instances />));
    await waitFor(() => {
      expect(screen.getByTestId('instances-empty-state')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('instance-add-ghost')).toBeNull();
  });

  it('respects instance filter for hero selection', async () => {
    const ctx = { filter: '4k', setFilter: vi.fn() };
    renderWithProviders(
      <InstanceFilterCtx.Provider value={ctx}>
        <Instances />
      </InstanceFilterCtx.Provider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('instance-hero-4k')).toBeInTheDocument();
    });
    expect(screen.getByTestId('instance-row-homelab')).toBeInTheDocument();
  });
});
