import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Scans } from './Scans';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/scans', search: '', assign: vi.fn() },
  });
});

afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

function scanFixture(over: Partial<Record<string, unknown>> = {}) {
  return {
    id: '7b3d4a92-1234-4abc-9def-000000000001',
    instance: 'alpha',
    trigger: 'cron',
    status: 'completed',
    started_at: new Date(Date.now() - 60_000).toISOString(),
    finished_at: new Date().toISOString(),
    series_scanned: 12,
    grabs_performed: 2,
    grabs_failed: 0,
    ...over,
  };
}

describe('<Scans />', () => {
  it('renders table rows from useScans', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            scanFixture(),
            scanFixture({ id: 'aaaaaaaa-1234-4abc-9def-000000000002', instance: 'beta' }),
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans' });
    expect(await screen.findByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
  });

  it('renders EmptyState with Clear filters action when filter is set and items=[]', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans?status=failed' });
    expect(await screen.findByText(/no scans match your filters/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /clear filters/i })).toBeInTheDocument();
  });

  it('toggling the status select writes to the URL', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans' });
    const statusTrigger = await screen.findByRole('combobox', { name: /any status/i });
    await userEvent.click(statusTrigger);
    await userEvent.click(await screen.findByRole('option', { name: /failed/i }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^clear$/i })).toBeEnabled(),
    );
  });
});
