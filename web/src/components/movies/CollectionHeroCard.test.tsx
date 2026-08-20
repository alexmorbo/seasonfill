// CollectionHeroCard — compact hero-right movie-collection card. Shares its
// data/mutation logic with `MovieCollectionBlock` via `useCollectionCardState`
// (see `web/src/hooks/useCollectionCardState.ts`); this test focuses on the
// card's own rendering: poster (image vs monogram fallback), name, monitor
// toggle and the add-all dialog wiring.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { CollectionHeroCard } from './CollectionHeroCard';

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
  poster: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  radarr_monitored: false,
  parts: [],
};
const COLLECTION_NO_POSTER = { ...COLLECTION, poster: null };
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
      return j({ requested: 1, added: 1, already_present: 0, failed: 0, parts: [] });
    }
    if (url.includes('/collections/')) return j(collection);
    return j({});
  });
  globalThis.fetch = fetchMock as typeof fetch;
}

function renderCard(instance = 'radarr-main') {
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
          <CollectionHeroCard tmdbCollectionId={726871} instance={instance} />
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
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/movies/438631', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<CollectionHeroCard />', () => {
  it('renders the poster image (not the monogram fallback) when the collection has a poster hash', async () => {
    installFetch();
    renderCard();
    expect(await screen.findByTestId('movie-collection-hero-card')).toBeInTheDocument();
    const poster = screen.getByTestId('movie-collection-hero-poster');
    const img = poster.querySelector('[data-testid="media-image-img"]') as HTMLImageElement | null;
    expect(img).not.toBeNull();
    expect(img?.getAttribute('src')).toBe(`/api/v1/media/${COLLECTION.poster}`);
    // Above-the-fold hero thumbnail — must not rely on native lazy-loading
    // (regression for the blank/white poster bug, see MediaImage `eager`).
    expect(img?.getAttribute('loading')).toBe('eager');
    expect(screen.getByTestId('movie-collection-hero-name')).toHaveTextContent('Dune Collection');
  });

  it('renders the monogram fallback cleanly (not a blank box) when the collection has no poster', async () => {
    installFetch(COLLECTION_NO_POSTER);
    renderCard();
    const poster = await screen.findByTestId('movie-collection-hero-poster');
    expect(poster.querySelector('[data-testid="media-image-img"]')).toBeNull();
    const monogram = poster.querySelector('[data-testid="monogram-fallback"]');
    expect(monogram).not.toBeNull();
    expect(monogram?.textContent).toBeTruthy();
  });

  it('fires the monitor PUT when the toggle is switched on', async () => {
    installFetch();
    renderCard();
    const toggle = await screen.findByTestId('movie-collection-hero-toggle');
    fireEvent.click(toggle);
    await waitFor(() => expect(monitorBodies.length).toBe(1));
    expect(monitorBodies[0]).toEqual({ instance_name: 'radarr-main' });
  });

  it('opens the add-all dialog', async () => {
    installFetch();
    renderCard();
    fireEvent.click(await screen.findByTestId('movie-collection-hero-add-all'));
    expect(await screen.findByTestId('movie-collection-hero-add-all-dialog')).toBeInTheDocument();
  });
});
