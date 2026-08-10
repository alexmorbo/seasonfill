import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { BlocklistTab } from './BlocklistTab';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string, init?: unknown) => mockApi(p, init) };
});

const baseRows = [
  { id: 1, kind: 'tmdb', ref_id: 42, title: 'Severance', poster_hash: 'abc' },
  { id: 2, kind: 'keyword', ref_id: 99, label: 'anime' },
];

// Stateful server double: the list GET reflects deletes so the post-mutation
// invalidate/refetch doesn't resurrect an optimistically-removed row.
let rows: typeof baseRows;

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}><BlocklistTab /></I18nextProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockApi.mockReset();
  rows = baseRows.map((r) => ({ ...r }));
  mockApi.mockImplementation((p: string, init?: { method?: string }) => {
    if (p === '/discovery/blocklist' && (!init || init.method === undefined)) return Promise.resolve(rows);
    if (p.startsWith('/discovery/blocklist/')) { // DELETE /discovery/blocklist/:id
      const id = Number(p.split('/').pop());
      rows = rows.filter((r) => r.id !== id);
      return Promise.resolve(undefined);
    }
    if (p === '/discovery/blocklist') return Promise.resolve({ id: 3, kind: 'keyword', ref_id: 7, label: 'time travel' }); // POST
    if (p.startsWith('/discovery/keyword-search')) return Promise.resolve([{ id: 7, name: 'time travel' }]);
    return Promise.resolve(undefined);
  });
});

describe('<BlocklistTab />', () => {
  it('renders tmdb series rows and keyword rows', async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText('Severance')).toBeInTheDocument());
    expect(screen.getByText('anime')).toBeInTheDocument();
    expect(screen.getByTestId('blocklist-tmdb-1')).toBeInTheDocument();
    expect(screen.getByTestId('blocklist-keyword-2')).toBeInTheDocument();
  });

  it('remove fires DELETE and optimistically drops the row', async () => {
    const user = userEvent.setup();
    renderTab();
    await waitFor(() => expect(screen.getByText('Severance')).toBeInTheDocument());
    await user.click(screen.getByTestId('blocklist-remove-1'));
    await waitFor(() => expect(screen.queryByText('Severance')).toBeNull());
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist/1',
      expect.objectContaining({ method: 'DELETE' }));
  });

  it('keyword typeahead debounces, searches, and POSTs the pick', async () => {
    const user = userEvent.setup();
    renderTab();
    await waitFor(() => expect(screen.getByText('anime')).toBeInTheDocument());

    const input = screen.getByTestId('keyword-search-input');
    await user.type(input, 'time');
    // Debounced GET resolves → suggestion appears.
    const sug = await screen.findByTestId('keyword-suggestion-7');
    expect(mockApi.mock.calls.some(([p]) => (p as string).startsWith('/discovery/keyword-search?q=time'))).toBe(true);

    await user.click(sug);
    await waitFor(() => expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist',
      expect.objectContaining({ method: 'POST', body: { kind: 'keyword', ref_id: 7, label: 'time travel' } })));
  });
});
