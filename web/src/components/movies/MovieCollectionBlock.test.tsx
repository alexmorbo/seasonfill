// Ф6-R-6b Wave B — MovieCollectionBlock: parts grid + per-part badges, the
// enable-only Radarr monitor toggle (PUT), and the add-all-missing dialog
// (POST + result toast). Radix Selects can't be driven in JSDOM, so the
// add-all submit relies on the QP/RF auto-seed from the radarr instance
// defaults.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { MovieCollectionBlock } from './MovieCollectionBlock';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) },
}));

const fetchMock = vi.fn();
const origFetch = globalThis.fetch;

const COLLECTION = {
  tmdb_collection_id: 726871,
  name: 'Dune Collection',
  poster: null,
  radarr_monitored: false,
  parts: [
    { tmdb_id: 438631, title: 'Dune', year: 2021, in_library: true, movie_id: 1 },
    { tmdb_id: 693134, title: 'Dune: Part Two', year: 2024, in_library: false, movie_id: 2 },
  ],
};
const INSTANCES = {
  instances: [{
    name: 'radarr-main', type: 'radarr', health: 'Available', mode: 'auto',
    default_quality_profile_id: 6, default_root_folder_path: '/movies',
  }],
};
const QP_PAYLOAD = {
  items: [{ id: 6, name: 'HD-1080p' }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'radarr-main',
};
const RF_PAYLOAD = {
  items: [{ id: 9, path: '/movies', accessible: true, free_space: 100 }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'radarr-main',
};

const monitorBodies: unknown[] = [];
const addAllBodies: unknown[] = [];

function j(b: unknown, s = 200): Response {
  return new Response(JSON.stringify(b), { status: s, headers: { 'Content-Type': 'application/json' } });
}

function installFetch(collection: object = COLLECTION) {
  fetchMock.mockImplementation(async (input: string | URL | Request, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString();
    const method = (init?.method ?? 'GET').toUpperCase();
    if (url.endsWith('/api/v1/admin/instances')) return j(INSTANCES);
    if (url.endsWith('/quality-profiles')) return j(QP_PAYLOAD);
    if (url.endsWith('/root-folders')) return j(RF_PAYLOAD);
    if (url.includes('/collections/') && url.endsWith('/monitor') && method === 'PUT') {
      if (init?.body) monitorBodies.push(JSON.parse(init.body as string));
      return new Response(null, { status: 204 });
    }
    if (url.includes('/collections/') && url.endsWith('/add-all-missing') && method === 'POST') {
      if (init?.body) addAllBodies.push(JSON.parse(init.body as string));
      return j({ requested: 1, added: 1, already_present: 0, failed: 0, parts: [] });
    }
    if (url.includes('/collections/')) return j(collection);
    return j({});
  });
  globalThis.fetch = fetchMock as typeof fetch;
}

function renderBlock(instance = 'radarr-main') {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <MovieCollectionBlock tmdbCollectionId={726871} instance={instance} />
        </MemoryRouter>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

beforeEach(() => {
  fetchMock.mockReset();
  toastSuccess.mockReset();
  toastError.mockReset();
  monitorBodies.length = 0;
  addAllBodies.length = 0;
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/movies/438631', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<MovieCollectionBlock />', () => {
  it('renders the parts grid with per-part library-membership badges', async () => {
    installFetch();
    renderBlock();
    expect(await screen.findByTestId('movie-collection-block')).toBeInTheDocument();
    expect(screen.getByTestId('movie-collection-name')).toHaveTextContent('Dune Collection');
    expect(screen.getByTestId('movie-collection-part-438631')).toBeInTheDocument();
    expect(screen.getByTestId('movie-collection-part-badge-438631'))
      .toHaveTextContent(i18n.t('movieCollection.part.inLibrary'));
    expect(screen.getByTestId('movie-collection-part-badge-693134'))
      .toHaveTextContent(i18n.t('movieCollection.part.missing'));
  });

  it('omits the empty year parens when a part has no year but keeps a valued one', async () => {
    installFetch({
      ...COLLECTION,
      parts: [
        { tmdb_id: 111, title: 'Gladiator III', year: null, in_library: false, movie_id: 3 },
        { tmdb_id: 222, title: 'Gladiator', year: 2000, in_library: true, movie_id: 4 },
      ],
    });
    renderBlock();

    const nullPart = await screen.findByTestId('movie-collection-part-111');
    expect(nullPart).toHaveTextContent('Gladiator III');
    expect(nullPart.textContent ?? '').not.toContain('()');

    const valuedPart = screen.getByTestId('movie-collection-part-222');
    expect(valuedPart).toHaveTextContent('(2000)');
  });

  it('fires the monitor PUT when the toggle is switched on', async () => {
    installFetch();
    renderBlock();
    const toggle = await screen.findByTestId('movie-collection-monitor-toggle');
    expect((toggle as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(toggle);
    await waitFor(() => expect(monitorBodies.length).toBe(1));
    expect(monitorBodies[0]).toEqual({ instance_name: 'radarr-main' });
  });

  it('runs add-all-missing and toasts the result counts', async () => {
    installFetch();
    renderBlock();
    fireEvent.click(await screen.findByTestId('movie-collection-add-all-open'));

    const submit = await screen.findByTestId('movie-collection-add-all-submit');
    await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(submit);

    await waitFor(() => expect(addAllBodies.length).toBe(1));
    expect(addAllBodies[0]).toEqual({
      instance_name: 'radarr-main',
      quality_profile_id: 6,
      root_folder_path: '/movies',
      minimum_availability: 'released',
      monitored: true,
      search_on_add: true,
    });
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
  });
});
