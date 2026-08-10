import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DiscoveryRail } from './DiscoveryRail';
import type { DiscoveryRow } from '@/api/discoveryRows';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string, init?: unknown) => mockApi(p, init) };
});

// Mock sonner so the flow is deterministic (no portal/timer dependency); the
// full Toaster-rendered Undo interaction is Playwright-verified (see story).
const toastFn = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign((...a: unknown[]) => toastFn(...a), { error: vi.fn() }),
}));

const items = [
  { series_id: 31, tmdb_id: 1, title: 'Rick and Morty', year: 2013, poster_hash: 'abc', tmdb_rating: 8.7, in_library_instances: [] },
  { series_id: 32, tmdb_id: 2, title: 'Severance', year: 2022, poster_hash: 'def', tmdb_rating: 8.4, in_library_instances: [] },
];

function row(): DiscoveryRow {
  return { row_type: 'trending', source: 'tmdb_discover', media_type: 'tv', params: {}, position: 0, enabled: true, title: 'Тренды' };
}

function renderRail() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter><DiscoveryRail row={row()} /></MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockApi.mockReset();
  toastFn.mockReset();
  mockApi.mockImplementation((p: string) => {
    if (p.startsWith('/discovery/blocklist')) return Promise.resolve({ id: 77, kind: 'tmdb', ref_id: 1 });
    return Promise.resolve({ items });
  });
});

describe('DiscoveryRail hide flow', () => {
  it('kebab → hide optimistically removes the card + POSTs the tmdb ref', async () => {
    const user = userEvent.setup();
    renderRail();
    await waitFor(() => expect(screen.getByText('Rick and Morty')).toBeInTheDocument());

    // Open the first card's kebab (Rick and Morty) and hide it.
    const triggers = screen.getAllByTestId('discovery-card-menu-trigger');
    await user.click(triggers[0]!);
    await user.click(await screen.findByTestId('discovery-card-hide'));

    // Optimistic removal: card gone, sibling remains.
    await waitFor(() => expect(screen.queryByText('Rick and Morty')).toBeNull());
    expect(screen.getByText('Severance')).toBeInTheDocument();

    // POST fired for the hidden tmdb_id.
    expect(mockApi.mock.calls.some(([p, init]) =>
      (p as string) === '/discovery/blocklist' &&
      (init as { body?: { ref_id?: number } })?.body?.ref_id === 1)).toBe(true);

    // Undo toast raised with an action.
    await waitFor(() => expect(toastFn).toHaveBeenCalledTimes(1));
  });
});
