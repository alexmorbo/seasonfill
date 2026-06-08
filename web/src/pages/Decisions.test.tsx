import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { Decisions } from './Decisions';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { renderPageWithTitle } from '@/test-utils-title';

const SAMPLE = [
  { id: 'd1', instance: 'homelab', series_id: 1, series_title: 'Foundation',
    season_number: 3, category: 'nothing_found', decision: 'skip',
    reason: 'nothing_above_threshold',
    created_at: '2026-06-07T10:00:00Z', scan_run_id: 's1' },
  { id: 'd2', instance: 'homelab', series_id: 2, series_title: 'For All Mankind',
    season_number: 5, category: 'action_taken', decision: 'grab',
    reason: 'upgrade_available',
    created_at: '2026-06-07T09:00:00Z', scan_run_id: 's2' },
  { id: 'd3', instance: 'homelab', series_id: 3, series_title: 'OK Series',
    season_number: 1, category: 'all_complete', decision: 'skip',
    reason: 'all_complete',
    created_at: '2026-06-07T08:00:00Z', scan_run_id: 's3' },
];

const origFetch = globalThis.fetch;

function renderPage(initialPath = '/decisions') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <InstanceFilterCtx.Provider value={{ filter: 'homelab', setFilter: () => {} }}>
          <PageTitleProvider defaultTitle="__INITIAL__">
            <MemoryRouter initialEntries={[initialPath]}>
              <Routes>
                <Route path="/decisions" element={<Decisions />} />
              </Routes>
            </MemoryRouter>
          </PageTitleProvider>
        </InstanceFilterCtx.Provider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

let decisionsPayload: { items: unknown[] } = { items: SAMPLE };

beforeEach(() => {
  decisionsPayload = { items: SAMPLE };
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
    const u = typeof url === 'string' ? url : url.toString();
    if (u.includes('/instances')) {
      return new Response(JSON.stringify({
        instances: [{ name: 'homelab' }, { name: '4k' }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (u.includes('/decisions')) {
      return new Response(JSON.stringify(decisionsPayload), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('/decisions page (integration)', () => {
  it('renders header + filter bar + accordion', async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('decisions-series-accordion')).toBeInTheDocument();
    });
    expect(screen.getByText('Foundation')).toBeInTheDocument();
  });

  it('first-run state renders when zero decisions exist', async () => {
    decisionsPayload = { items: [] };
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('decisions-first-run-state')).toBeInTheDocument();
    });
  });

  it('empty-filter state renders when filter matches nothing', async () => {
    renderPage('/decisions?q=NoSeriesMatchesThis');
    await waitFor(() => {
      expect(screen.getByTestId('decisions-empty-state')).toBeInTheDocument();
    });
  });

  it('season-row click opens the drawer via ?series=&season= URL', async () => {
    renderPage();
    const accordion = await screen.findByTestId('decisions-series-accordion');
    const seasonRows = await within(accordion).findAllByTestId('decisions-season-row');
    await userEvent.click(seasonRows[0]!);
    await waitFor(() => {
      expect(screen.getByTestId('decisions-drawer')).toBeInTheDocument();
    });
  });

  it('reset clears all URL filters', async () => {
    renderPage('/decisions?q=foundation&category=none&window=30d&sort=stuck-first');
    const resetBtn = await screen.findByRole('button', { name: /reset|сброс/i });
    expect(resetBtn).not.toBeDisabled();
    await userEvent.click(resetBtn);
    const input = screen.getByPlaceholderText(/series|сериал/i) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe(''));
  });

  it('sets the topbar page title via useSetPageTitle', async () => {
    const { getTitle } = renderPageWithTitle(<Decisions />, { route: '/decisions' });
    await waitFor(() => {
      expect(getTitle()).toBe(i18n.t('decisions.title'));
    });
  });
});
