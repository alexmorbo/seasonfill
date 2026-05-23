import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Decisions } from './Decisions';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/decisions', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

function dec(over: Partial<Record<string, unknown>> = {}) {
  return {
    id: 'd_001',
    instance: 'alpha',
    series_title: 'Severance',
    season_number: 1,
    decision: 'grab',
    reason: 'grab_selected_dry_run',
    category: 'action_taken',
    candidates_count: 3,
    scan_run_id: '7b3d4a92-1234-4abc-9def-000000000001',
    created_at: new Date().toISOString(),
    ...over,
  };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

describe('<Decisions />', () => {
  it('renders rows from useDecisions with category chips', async () => {
    globalThis.fetch = vi.fn().mockImplementation(async () =>
      json({
        items: [
          dec(),
          dec({
            id: 'd_002',
            series_title: 'Andor',
            decision: 'skip',
            category: 'all_complete',
          }),
        ],
      }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Decisions />), { route: '/decisions' });
    expect(await screen.findByText('Severance')).toBeInTheDocument();
    expect(screen.getByText('Andor')).toBeInTheDocument();
    // Both chips present (compact variant — `aria-label="Category: ..."`).
    expect(screen.getByLabelText(/category: action taken/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/category: all complete/i)).toBeInTheDocument();
  });

  it('opens drawer when a row is clicked', async () => {
    globalThis.fetch = vi
      .fn()
      .mockImplementation(async () => json({ items: [dec()] })) as typeof fetch;
    renderWithProviders(wrap(<Decisions />), { route: '/decisions' });
    await userEvent.click(
      await screen.findByRole('button', { name: /open decision d_001/i }),
    );
    await waitFor(() => expect(screen.getByText(/Decision tree/i)).toBeInTheDocument());
  });

  it('shows empty state with Clear filters when filters set', async () => {
    globalThis.fetch = vi
      .fn()
      .mockImplementation(async () => json({ items: [] })) as typeof fetch;
    renderWithProviders(wrap(<Decisions />), { route: '/decisions?outcome=skip' });
    expect(await screen.findByText(/no decisions match/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /clear filters/i })).toBeInTheDocument();
  });
});
