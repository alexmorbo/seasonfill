// Ф6-R-6b Wave B — exercises the AddToRadarrModal surface reachable from
// JSDOM. Radix Selects can't be driven in JSDOM, so submit relies on the
// per-instance QP/RF auto-seed (default_quality_profile_id /
// default_root_folder_path) firing — mirroring AddToSonarrModal.test.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { AddToRadarrModal } from './AddToRadarrModal';
import type { AddToRadarrTarget } from './add-to-radarr-context';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) },
}));

const fetchMock = vi.fn();
const origFetch = globalThis.fetch;

function mkClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
}

function renderModal(targetOverrides: Partial<AddToRadarrTarget> = {}) {
  const target: AddToRadarrTarget = { title: 'Dune', tmdbId: 438631, ...targetOverrides };
  const qc = mkClient();
  const onClose = vi.fn();
  const utils = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <AddToRadarrModal target={target} onClose={onClose} />
        </MemoryRouter>
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return { ...utils, qc, onClose };
}

// Two instances: a sonarr (must be filtered out) + a radarr with QP/RF defaults.
const INSTANCES_BOTH = {
  instances: [
    { name: 'sonarr-main', type: 'sonarr', health: 'Available', mode: 'auto' },
    {
      name: 'radarr-main', type: 'radarr', health: 'Available', mode: 'auto',
      default_quality_profile_id: 6, default_root_folder_path: '/movies',
    },
  ],
};
const INSTANCES_SONARR_ONLY = {
  instances: [{ name: 'sonarr-main', type: 'sonarr', health: 'Available', mode: 'auto' }],
};
const QP_PAYLOAD = {
  items: [{ id: 6, name: 'HD-1080p' }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'radarr-main',
};
const RF_PAYLOAD = {
  items: [{ id: 9, path: '/movies', accessible: true, free_space: 100 }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'radarr-main',
};

const capturedAddBodies: unknown[] = [];

function routeResponse(url: string, init: RequestInit | undefined, instancesPayload: object): Response {
  const j = (b: unknown, s = 200) =>
    new Response(JSON.stringify(b), { status: s, headers: { 'Content-Type': 'application/json' } });
  if (url.endsWith('/api/v1/admin/instances')) return j(instancesPayload);
  if (url.endsWith('/quality-profiles')) return j(QP_PAYLOAD);
  if (url.endsWith('/root-folders')) return j(RF_PAYLOAD);
  if (url.endsWith('/discovery/add-to-radarr')) {
    if (init?.body) capturedAddBodies.push(JSON.parse(init.body as string));
    return j({ radarr_movie_id: 42, instance_name: 'radarr-main', already_added: false });
  }
  return j({});
}

function installFetch(instancesPayload: object) {
  fetchMock.mockImplementation(async (input: string | URL | Request, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString();
    return routeResponse(url, init, instancesPayload);
  });
  globalThis.fetch = fetchMock as typeof fetch;
}

beforeEach(() => {
  fetchMock.mockReset();
  toastSuccess.mockReset();
  toastError.mockReset();
  capturedAddBodies.length = 0;
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/movies/438631', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<AddToRadarrModal />', () => {
  it('renders the movie title in the modal header', async () => {
    installFetch(INSTANCES_BOTH);
    renderModal();
    expect(await screen.findByTestId('add-to-radarr-modal')).toBeInTheDocument();
    expect(screen.getByText(/Dune/)).toBeInTheDocument();
  });

  it('shows the no-radarr empty state when only sonarr instances exist', async () => {
    installFetch(INSTANCES_SONARR_ONLY);
    renderModal();
    expect(await screen.findByTestId('add-to-radarr-no-instances')).toBeInTheDocument();
    expect((screen.getByTestId('add-to-radarr-submit') as HTMLButtonElement).disabled).toBe(true);
  });

  it('filters instances to radarr targets (no sonarr in the picker)', async () => {
    installFetch(INSTANCES_BOTH);
    renderModal();
    // The radarr instance trigger renders (not the no-radarr message).
    expect(await screen.findByTestId('add-to-radarr-instance')).toBeInTheDocument();
    expect(screen.queryByTestId('add-to-radarr-no-instances')).toBeNull();
  });

  it('auto-seeds QP/RF and submits the exact body with minimum_availability released', async () => {
    installFetch(INSTANCES_BOTH);
    renderModal();

    const submit = await screen.findByTestId('add-to-radarr-submit');
    await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false));

    fireEvent.click(submit);

    await waitFor(() => expect(capturedAddBodies.length).toBe(1));
    expect(capturedAddBodies[0]).toEqual({
      instance_name: 'radarr-main',
      tmdb_id: 438631,
      quality_profile_id: 6,
      root_folder_path: '/movies',
      minimum_availability: 'released',
      search_on_add: true,
    });
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
  });
});
