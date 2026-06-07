import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Scans } from './Scans';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import { PageTitleProvider } from '@/components/shell/page-title-context';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/scans', search: '', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

const wrap = (ui: ReactElement) => (
  <PageTitleProvider defaultTitle="Scans">
    <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
  </PageTitleProvider>
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
    candidates_found: 4,
    grabs_performed: 2,
    grabs_failed: 0,
    ...over,
  };
}

describe('<Scans /> redesign', () => {
  it('renders dense table rows from useScans with st-pill status column', async () => {
    globalThis.fetch = vi.fn(() => Promise.resolve(
      new Response(JSON.stringify({ items: [
        scanFixture(),
        scanFixture({ id: 'aaaa-0002', instance: 'beta', status: 'failed', trigger: 'manual' }),
      ] }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans' });
    expect(await screen.findByTestId('scans-table')).toBeInTheDocument();
    const rows = screen.getAllByTestId('scans-row');
    expect(rows).toHaveLength(2);
    expect(rows[0]!.querySelector('[data-status-kind="ok"]')).toBeTruthy();
    expect(rows[1]!.querySelector('[data-status-kind="fail"]')).toBeTruthy();
  });

  it('applies status filter via wire query', async () => {
    const captured: string[] = [];
    globalThis.fetch = vi.fn((url) => {
      captured.push(typeof url === 'string' ? url : url.toString());
      return Promise.resolve(new Response(JSON.stringify({ items: [] }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }));
    }) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans?status=failed' });
    await waitFor(() => expect(captured.some((u) => u.includes('status=failed'))).toBe(true));
  });

  it('applies trigger filter client-side (no wire param)', async () => {
    const captured: string[] = [];
    globalThis.fetch = vi.fn((url) => {
      captured.push(typeof url === 'string' ? url : url.toString());
      return Promise.resolve(new Response(JSON.stringify({ items: [
        scanFixture({ id: 'a', trigger: 'cron' }),
        scanFixture({ id: 'b', trigger: 'manual' }),
      ] }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans?trigger=manual' });
    await screen.findByTestId('scans-table');
    expect(screen.getAllByTestId('scans-row')).toHaveLength(1);
    // No `trigger=` on the wire — confirms B9 fallback.
    expect(captured.every((u) => !u.includes('trigger='))).toBe(true);
  });

  it('shows ScansFirstRunState when zero scans exist and no filters', async () => {
    globalThis.fetch = vi.fn(() => Promise.resolve(
      new Response(JSON.stringify({ items: [] }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    )) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans' });
    expect(await screen.findByTestId('scans-first-run')).toBeInTheDocument();
  });

  it('shows ScansEmptyState with reset CTA when filter is set and items=[]', async () => {
    globalThis.fetch = vi.fn(() => Promise.resolve(
      new Response(JSON.stringify({ items: [] }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    )) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans?status=failed' });
    expect(await screen.findByTestId('scans-empty-state')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /сбросить|reset/i }).length).toBeGreaterThan(0);
  });

  it('reset clears all URL params back to defaults', async () => {
    const user = userEvent.setup();
    globalThis.fetch = vi.fn(() => Promise.resolve(
      new Response(JSON.stringify({ items: [scanFixture()] }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    )) as typeof fetch;
    renderWithProviders(wrap(<Scans />), { route: '/scans?status=failed&trigger=manual&window=24h' });
    await screen.findByTestId('scans-filters-bar');
    const reset = screen.getByTestId('scans-filters-reset');
    expect(reset).not.toBeDisabled();
    await user.click(reset);
    await waitFor(() => {
      expect(window.location.search).not.toContain('status=');
      expect(window.location.search).not.toContain('trigger=');
      expect(window.location.search).not.toContain('window=');
    });
  });
});
