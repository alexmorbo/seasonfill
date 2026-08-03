// S5 / ADR-0008 regression: the Add-to-Sonarr modal is decoupled from the
// discovery card. See the story's §7.1 for why the deterministic structural
// test — not the Radix-driven one — is the real fail-on-old / pass-on-new
// guard in jsdom.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { SeriesCard } from '@/components/series/SeriesCard';
import { AddToSonarrProvider } from './AddToSonarrProvider';
import type { DiscoverySeriesItem } from '@/api/discovery';

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

const origFetch = globalThis.fetch;

function mkClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
}

const ME_PAYLOAD = {
  id: 1, username: 'alex', email: null, role: 'admin',
  auth_mode: 'forms', avatar_mode: 'auto', avatar_resolved_mode: 'monogram',
  avatar_hash: 'h', preferred_language: 'en-US',
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
const LOOKUP_PAYLOAD = {
  items: [
    { season_number: 0, episode_count: 2, monitored: false },
    { season_number: 1, episode_count: 11, monitored: true },
  ],
  title: 'Rick and Morty', year: 2013, overview: 'x', image_url: '',
  tvdb_id: 81189, tmdb_id: 1399, instance_name: 'main',
};

function routeResponse(url: string): Response {
  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body),
      { status, headers: { 'Content-Type': 'application/json' } });
  if (url.endsWith('/api/v1/me')) return json(ME_PAYLOAD);
  if (url.endsWith('/api/v1/admin/instances')) return json(INSTANCES_PAYLOAD);
  if (url.endsWith('/quality-profiles')) return json(QP_PAYLOAD);
  if (url.endsWith('/root-folders')) return json(RF_PAYLOAD);
  if (url.includes('/sonarr-lookup')) return json(LOOKUP_PAYLOAD);
  if (url.endsWith('/discovery/add-to-sonarr')) {
    return json({
      sonarr_series_id: 99, instance_name: 'main',
      user_tag_label: 'sf-alex', user_tag_id: 1,
    });
  }
  return json({});
}

const ITEM: DiscoverySeriesItem = {
  series_id: 42, tmdb_id: 1399, tvdb_id: 81189,
  title: 'Rick and Morty', in_library_instances: [],
};

// Mirrors the real app tree: MemoryRouter > AddToSonarrProvider > Routes.
// The provider renders the modal as a SIBLING of the routed card — never a
// descendant of the card <Link>.
function renderTree() {
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={mkClient()}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter initialEntries={['/']}>
            <AddToSonarrProvider>
              <Routes>
                <Route
                  path="/"
                  element={
                    <SeriesCard
                      title="Rick and Morty"
                      seriesId={42}
                      addToSonarr={ITEM}
                    />
                  }
                />
                <Route
                  path="/series/:id"
                  element={<div data-testid="series-page" />}
                />
              </Routes>
            </AddToSonarrProvider>
          </MemoryRouter>
        </TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/', assign: vi.fn() },
  });
  globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
    const url = typeof input === 'string' ? input : input.toString();
    return routeResponse(url);
  }) as typeof fetch;
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('Add-to-Sonarr modal decoupled from card (S5 regression)', () => {
  it('opening the modal from the card does not navigate', async () => {
    renderTree();
    fireEvent.click(screen.getByTestId('add-to-sonarr-button'));
    expect(await screen.findByTestId('add-to-sonarr-modal'))
      .toBeInTheDocument();
    // The card's <Link> must not have navigated when the modal opened.
    expect(screen.queryByTestId('series-page')).toBeNull();
  });

  it('DETERMINISTIC: a bubbling click inside the open modal never reaches the card Link', async () => {
    renderTree();
    fireEvent.click(screen.getByTestId('add-to-sonarr-button'));
    const modal = await screen.findByTestId('add-to-sonarr-modal');

    // A React synthetic click that bubbles up from inside the modal. In the
    // OLD nested structure this bubbles to the card <Link> (guard removed) →
    // navigation. In the NEW structure the modal is a provider sibling, so no
    // <Link> is in its React ancestry → no navigation.
    fireEvent.click(modal);
    fireEvent.click(screen.getByTestId('add-to-sonarr-form'));

    expect(screen.queryByTestId('series-page')).toBeNull();
    expect(screen.getByTestId('add-to-sonarr-modal')).toBeInTheDocument();
  });

  it('FAITHFUL: opening a Radix Select and picking an option does not navigate', async () => {
    const user = userEvent.setup();
    renderTree();
    fireEvent.click(screen.getByTestId('add-to-sonarr-button'));
    await screen.findByTestId('add-to-sonarr-modal');

    // Wait for instance auto-select → quality-profile query resolves and the
    // qp trigger becomes enabled.
    await waitFor(() => {
      expect(screen.getByTestId('add-to-sonarr-qp')).not.toBeDisabled();
    });

    // The exact interaction that broke live: open the Radix Select and click
    // an item, which tears the popover down mid-click.
    await user.click(screen.getByTestId('add-to-sonarr-qp'));
    await user.click(await screen.findByRole('option', { name: 'HD-1080p' }));

    expect(screen.queryByTestId('series-page')).toBeNull();
    expect(screen.getByTestId('add-to-sonarr-modal')).toBeInTheDocument();
  });

  it('clicking the card poster (outside the modal) DOES navigate to /series/:id', async () => {
    const user = userEvent.setup();
    renderTree();
    // No modal open — a normal card click routes as before.
    await user.click(screen.getByTestId('series-card-title'));
    expect(await screen.findByTestId('series-page')).toBeInTheDocument();
  });
});
