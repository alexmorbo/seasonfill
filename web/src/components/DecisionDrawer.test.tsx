import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { DecisionDrawer } from './DecisionDrawer';
import type { Decision } from '@/lib/decisions';
import { DtoDecisionCategory, DtoDecisionDecision } from '@/api/schema';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const ctxValue = { filter: null, setFilter: vi.fn() };

const origFetch = globalThis.fetch;
const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/decisions', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

const eligible: Decision = {
  id: 'dec-eligible', instance: 'alpha', series_title: 'Severance',
  season_number: 2, decision: DtoDecisionDecision.grab, selected_guid: 'g-1',
  dry_run_would_grab: true, reason: 'grab_selected_dry_run',
  category: DtoDecisionCategory.action_taken, candidates_count: 16, releases_found: 16,
  existing_count: 1, missing_count: 9,
  scan_run_id: '7b3d4a92-1234-4abc-9def-000000000001',
  created_at: new Date().toISOString(),
};

function renderDrawer(d: Decision) {
  return renderWithProviders(
    <InstanceFilterCtx.Provider value={ctxValue}>
      <DecisionDrawer id={d.id ?? null} open onOpenChange={vi.fn()} rows={[d]} />
    </InstanceFilterCtx.Provider>,
  );
}

describe('<DecisionDrawer /> Grab now', () => {
  it('shows Grab now button for eligible decisions', async () => {
    globalThis.fetch = vi.fn() as typeof fetch;
    renderDrawer(eligible);
    expect(await screen.findByRole('button', { name: /grab now/i })).toBeInTheDocument();
    expect(screen.getByText(/force grab/i)).toBeInTheDocument();
  });

  it.each<readonly [string, Partial<Decision>]>([
    ['skip',           { decision: DtoDecisionDecision.skip, selected_guid: '', dry_run_would_grab: false }],
    ['dry_run=false',  { dry_run_would_grab: false }],
    ['empty guid',     { selected_guid: '' }],
  ])('hides Grab now for ineligible decisions (%s)', async (_label, over) => {
    globalThis.fetch = vi.fn() as typeof fetch;
    renderDrawer({ ...eligible, ...over });
    expect(await screen.findByText(/decision tree/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /grab now/i })).not.toBeInTheDocument();
  });

  it('POSTs to /decisions/:id/grab and surfaces success on click', async () => {
    // Array-form capture + URL-aware branching: useDecisions polling
    // refetch would overwrite a single-slot captured.url. See r1 fix log.
    type Captured = { urls: string[]; methods: string[]; bodies: string[] };
    const captured: Captured = { urls: [], methods: [], bodies: [] };
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      captured.urls.push(url);
      captured.methods.push(init?.method ?? 'GET');
      captured.bodies.push(typeof init?.body === 'string' ? init.body : '');
      if (url.endsWith('/grab') && init?.method === 'POST') {
        return jsonResp({
          id: '11111111-2222-3333-4444-555555555555',
          instance: 'alpha', release_guid: 'g-1', status: 'grabbed',
        });
      }
      return jsonResp({ items: [eligible], next_cursor: '' });
    }) as typeof fetch;

    renderDrawer(eligible);
    await userEvent.click(await screen.findByRole('button', { name: /grab now/i }));

    await waitFor(() => {
      const i = captured.urls.findIndex(
        (url, idx) => url.endsWith('/decisions/dec-eligible/grab') && captured.methods[idx] === 'POST',
      );
      expect(i).toBeGreaterThanOrEqual(0);
    });
    // Drawer stays open per Q-011b-2; inline success label present.
    await waitFor(() =>
      expect(screen.getByText(/grabbed: 11111111/i)).toBeInTheDocument(),
    );
  });
});
