import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Grabs } from './Grabs';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/grabs', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

function grab(over: Partial<Record<string, unknown>> = {}) {
  return {
    id: 'g_001',
    instance: 'alpha',
    series_title: 'Severance',
    release_title: 'Severance.S01E01.1080p.WEB-DL.x264',
    status: 'imported',
    indexer_name: 'tracker.x',
    attempts: 1,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

describe('<Grabs />', () => {
  it('renders rows from useGrabs', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(
        json({
          items: [grab(), grab({ id: 'g_002', series_title: 'Andor', status: 'import_failed' })],
        }),
      ) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs' });
    expect(await screen.findByText('Severance')).toBeInTheDocument();
    expect(screen.getByText('Andor')).toBeInTheDocument();
  });

  it('opens drawer with error block on failed grab', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      json({
        items: [grab({ status: 'import_failed', error_message: 'unable to import: file in use' })],
      }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs' });
    await userEvent.click(await screen.findByRole('button', { name: /open grab g_001/i }));
    await waitFor(() => expect(screen.getByText(/file in use/i)).toBeInTheDocument());
  });

  it('series header cycles asc → desc → unsorted', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(
        json({
          items: [
            grab({ id: 'g_001', series_title: 'Severance' }),
            grab({ id: 'g_002', series_title: 'Andor' }),
          ],
        }),
      ) as typeof fetch;
    const { container } = renderWithProviders(wrap(<Grabs />), { route: '/grabs' });
    const getSeriesCells = () =>
      Array.from(container.querySelectorAll('tbody tr')).map(
        (r) => r.querySelectorAll('td')[2]?.textContent ?? '',
      );
    await screen.findByText('Severance');
    expect(getSeriesCells()).toEqual(['Severance', 'Andor']);

    const header = screen
      .getAllByRole('button')
      .find((b) => b.getAttribute('data-sort-key') === 'series_title');
    expect(header).toBeDefined();

    await userEvent.click(header!);
    await waitFor(() => expect(getSeriesCells()).toEqual(['Andor', 'Severance']));
    expect(header!.getAttribute('data-sort-dir')).toBe('asc');

    await userEvent.click(header!);
    await waitFor(() => expect(getSeriesCells()).toEqual(['Severance', 'Andor']));
    expect(header!.getAttribute('data-sort-dir')).toBe('desc');

    await userEvent.click(header!);
    await waitFor(() => expect(header!.getAttribute('data-sort-dir')).toBe(''));
    expect(getSeriesCells()).toEqual(['Severance', 'Andor']);
  });

  it('client-side q filter narrows loaded rows', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(
        json({ items: [grab(), grab({ id: 'g_002', series_title: 'Andor' })] }),
      ) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?q=Andor' });
    await waitFor(() => expect(screen.getByText('Andor')).toBeInTheDocument());
    expect(screen.queryByText('Severance')).not.toBeInTheDocument();
  });
});
