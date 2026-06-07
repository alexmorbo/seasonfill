import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { DecisionsDrawer } from './DecisionsDrawer';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

function renderDrawer(open = true) {
  const client = makeClient();
  return render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <InstanceFilterCtx.Provider value={{ filter: 'homelab', setFilter: () => {} }}>
          <MemoryRouter>
            <DecisionsDrawer
              open={open}
              seriesId={42}
              seriesTitle="For All Mankind"
              seasonNumber={5}
              instance="homelab"
              window="7d"
              onOpenChange={() => {}}
            />
          </MemoryRouter>
        </InstanceFilterCtx.Provider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

describe('DecisionsDrawer', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const u = typeof url === 'string' ? url : url.toString();
      if (u.includes('/decisions')) {
        return new Response(JSON.stringify({
          items: [
            { id: 'd1', decision: 'grab',             reason: 'upgrade_available',
              created_at: '2026-06-07T19:32:00Z', scan_run_id: '7b3d0001abcd' },
            { id: 'd2', decision: 'blocked_cooldown', reason: 'blocked_cooldown',
              created_at: '2026-06-06T07:00:00Z', scan_run_id: '9d041100feed' },
          ],
        }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
    }) as typeof fetch;
  });
  afterEach(() => { globalThis.fetch = origFetch; });

  it('shows skeleton then renders timeline + summary chips', async () => {
    renderDrawer(true);
    expect(screen.getByTestId('drawer-skeleton')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId('decisions-timeline')).toBeInTheDocument();
    });
    expect(screen.getByText(/For All Mankind/)).toBeInTheDocument();
    expect(screen.getByText(/S05/)).toBeInTheDocument();
  });

  it('does not fetch when open=false', () => {
    renderDrawer(false);
    expect(screen.queryByTestId('decisions-drawer')).not.toBeInTheDocument();
  });
});
