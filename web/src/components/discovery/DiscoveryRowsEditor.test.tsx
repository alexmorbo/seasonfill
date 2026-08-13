import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DiscoveryRowsEditor } from './DiscoveryRowsEditor';
import type { DiscoveryRow } from '@/api/discoveryRows';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string, opts?: unknown) => mockApi(p, opts) };
});

// Genre picker fetches /discovery/genres — return one genre so the add flow can
// select it; the PUT echoes back the rows so the mutation resolves.
function primeApi() {
  mockApi.mockImplementation((path: string, opts?: { method?: string; body?: unknown }) => {
    if (path.startsWith('/discovery/genres')) {
      return Promise.resolve({ items: [{ id: 18, name: 'Драма' }] });
    }
    if (path.startsWith('/discovery/networks')) {
      return Promise.resolve({ items: [] });
    }
    if (path === '/discovery/rows' && opts?.method === 'PUT') {
      return Promise.resolve((opts.body as { rows: unknown }) ?? { rows: [] });
    }
    return Promise.resolve({ rows: [] });
  });
}

const initial: DiscoveryRow[] = [
  { row_type: 'trending', source: 'tmdb_discover', media_type: 'tv',
    params: {}, position: 0, enabled: true, title: 'Тренды' },
];

function renderEditor() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <DiscoveryRowsEditor initial={initial} onExit={vi.fn()} />
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

beforeEach(async () => {
  mockApi.mockReset();
  primeApi();
  await act(async () => { await i18n.changeLanguage('ru-RU'); });
});

afterEach(async () => {
  await act(async () => { await i18n.changeLanguage('en-US'); });
});

describe('<DiscoveryRowsEditor /> media-type selector', () => {
  it('renders both TV and Movies options in the media selector', async () => {
    const user = userEvent.setup();
    renderEditor();
    await user.click(screen.getByTestId('discovery-add-media'));
    await waitFor(() => {
      const opts = screen.getAllByRole('option').map((o) => o.textContent);
      expect(opts).toEqual(expect.arrayContaining(['Сериалы', 'Фильмы']));
    });
  });

  it('emits a row with media_type=movie when Movies is chosen', async () => {
    const user = userEvent.setup();
    renderEditor();

    // choose Movies in the media selector
    await user.click(screen.getByTestId('discovery-add-media'));
    await user.click(await screen.findByRole('option', { name: 'Фильмы' }));

    // pick a genre (default add type is 'genre')
    await user.click(screen.getByTestId('discovery-add-genre'));
    await user.click(await screen.findByRole('option', { name: 'Драма' }));

    // add the row, then save
    await user.click(screen.getByTestId('discovery-add-confirm'));
    await user.click(screen.getByTestId('discovery-edit-save'));

    await waitFor(() => {
      const putCall = mockApi.mock.calls.find((call) => {
        const [p, o] = call as [string, { method?: string } | undefined];
        return p === '/discovery/rows' && o?.method === 'PUT';
      });
      expect(putCall).toBeTruthy();
      const rows = (putCall![1] as { body: { rows: { media_type: string }[] } }).body.rows;
      expect(rows.some((r) => r.media_type === 'movie')).toBe(true);
    });
  });
});
