// Story 522 / N-4e — exercises the modal surface that is reachable
// from JSDOM. Radix Select widgets mount into a portal and react to
// real pointer events, which JSDOM cannot synthesise reliably, so this
// suite focuses on:
//   - title rendering with the series name
//   - tag preview (sf-{username}) from /me, including the sf-system
//     fallback for bypass-style usernames
//   - cancel button wiring
//   - submit gating when tvdb_id is missing
//
// The wire-shape contract and the success/error mutation paths are
// covered in api/__tests__/discovery.test.tsx (useAddToSonarr).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { AddToSonarrModal } from './AddToSonarrModal';
import type { DiscoverySeriesItem } from '@/api/discovery';

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
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

function renderModal(
  itemOverrides: Partial<DiscoverySeriesItem> = {},
) {
  const item: DiscoverySeriesItem = {
    series_id: 42,
    tmdb_id: 1399,
    tvdb_id: 81189,
    title: 'Rick and Morty',
    in_library_instances: [],
    ...itemOverrides,
  };
  const qc = mkClient();
  const onOpenChange = vi.fn();
  const utils = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <AddToSonarrModal
            open
            onOpenChange={onOpenChange}
            item={item}
          />
        </MemoryRouter>
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return { ...utils, qc, onOpenChange };
}

const ME_PAYLOAD = {
  id: 1, username: 'alex', email: null, role: 'admin',
  auth_mode: 'forms', avatar_mode: 'auto', avatar_resolved_mode: 'monogram',
  avatar_hash: 'h', preferred_language: 'en',
  idp_profile_url: null, oidc_subject: null, last_login_at: null,
};
const INSTANCES_PAYLOAD = {
  instances: [{ name: 'main', health: 'Available', mode: 'auto' }],
};
const QP_PAYLOAD = {
  items: [{ id: 6, name: 'HD-1080p' }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'main',
};
const RF_PAYLOAD = {
  items: [{ id: 9, path: '/tv', accessible: true, free_space: 100 }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'main',
};

function routeResponse(url: string, meOverride?: object): Response {
  if (url.endsWith('/api/v1/me')) {
    return new Response(JSON.stringify({ ...ME_PAYLOAD, ...meOverride }),
      { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  if (url.endsWith('/api/v1/admin/instances')) {
    return new Response(JSON.stringify(INSTANCES_PAYLOAD),
      { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  if (url.endsWith('/quality-profiles')) {
    return new Response(JSON.stringify(QP_PAYLOAD),
      { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  if (url.endsWith('/root-folders')) {
    return new Response(JSON.stringify(RF_PAYLOAD),
      { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  return new Response('{}',
    { status: 200, headers: { 'Content-Type': 'application/json' } });
}

beforeEach(() => {
  fetchMock.mockReset();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/discover', assign: vi.fn() },
  });
  fetchMock.mockImplementation(async (input: string | URL | Request) => {
    const url = typeof input === 'string' ? input : input.toString();
    return routeResponse(url);
  });
  globalThis.fetch = fetchMock as typeof fetch;
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('<AddToSonarrModal />', () => {
  it('renders the series title in the modal header', async () => {
    renderModal();
    expect(
      await screen.findByText(/Rick and Morty/),
    ).toBeInTheDocument();
  });

  it('previews sf-{username} from /me in the description', async () => {
    renderModal();
    await waitFor(() => {
      expect(screen.getByText(/sf-alex/)).toBeInTheDocument();
    });
  });

  it('previews sf-system for bypass-style usernames', async () => {
    fetchMock.mockImplementation(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input.toString();
      return routeResponse(url, { username: 'api-key' });
    });
    renderModal();
    await waitFor(() => {
      expect(screen.getByText(/sf-system/)).toBeInTheDocument();
    });
  });

  it('cancel button calls onOpenChange(false)', () => {
    const { onOpenChange } = renderModal();
    fireEvent.click(screen.getByTestId('add-to-sonarr-cancel'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('disables submit when tvdb_id is missing on the item', async () => {
    const qc = mkClient();
    const item = {
      series_id: 42, tmdb_id: 1399,
      title: 'Rick and Morty', in_library_instances: [],
    } as DiscoverySeriesItem;
    render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <MemoryRouter>
            <AddToSonarrModal
              open onOpenChange={vi.fn()} item={item}
            />
          </MemoryRouter>
        </QueryClientProvider>
      </I18nextProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('add-to-sonarr-submit')).toBeDisabled();
    });
  });

  it('disables submit when tvdb_id is zero', async () => {
    renderModal({ tvdb_id: 0 });
    await waitFor(() => {
      expect(screen.getByTestId('add-to-sonarr-submit')).toBeDisabled();
    });
  });

  // Story 523: when tvdb_id is missing the modal explains *why* Submit
  // is disabled — the BE projection now exposes tvdb_id for every
  // worker-hydrated row, so a missing value means the legacy stub
  // hasn't been re-enriched yet.
  it('shows the missing-tvdb info banner when tvdb_id is absent', async () => {
    const qc = mkClient();
    const item = {
      series_id: 42, tmdb_id: 1399,
      title: 'Rick and Morty', in_library_instances: [],
    } as DiscoverySeriesItem;
    render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <MemoryRouter>
            <AddToSonarrModal
              open onOpenChange={vi.fn()} item={item}
            />
          </MemoryRouter>
        </QueryClientProvider>
      </I18nextProvider>,
    );
    await waitFor(() => {
      expect(
        screen.getByTestId('add-to-sonarr-missing-tvdb'),
      ).toBeInTheDocument();
    });
  });

  it('hides the missing-tvdb banner on the happy path', async () => {
    renderModal({ tvdb_id: 81189 });
    await waitFor(() => {
      expect(screen.queryByTestId('add-to-sonarr-missing-tvdb'))
        .not.toBeInTheDocument();
    });
  });
});
