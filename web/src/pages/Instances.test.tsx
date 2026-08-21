import { type ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { Instances } from './Instances';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import i18n from '@/i18n';
import { renderPageWithTitle } from '@/test-utils-title';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/instances')) {
      return new Response(JSON.stringify({
        instances: [
          { name: 'homelab', type: 'sonarr', mode: 'auto', health: 'Available', last_check_at: new Date().toISOString(), transitions_count: 0, url: 'http://sonarr:80' },
          { name: '4k', mode: 'manual', health: 'Unreachable', last_check_at: new Date().toISOString(), transitions_count: 3, url: 'http://sonarr-4k:80', last_error: 'dial tcp — connection refused' },
          { name: 'films', type: 'radarr', mode: 'auto', health: 'Available', last_check_at: new Date().toISOString(), transitions_count: 0, url: 'http://radarr:7878' },
        ],
      }), { status: 200 });
    }
    if (url.includes('/counters')) {
      // Aggregate shape ({ items: InstanceCountersDTO[] }) — matches
      // useCountersAggregate's CountersAggregateDTO. A bare per-instance
      // body here (no `items` array) throws in InstanceHero's
      // `c24.data?.items.find(...)` once the query actually resolves;
      // previously-passing tests just never waited long enough to
      // observe it.
      return new Response(JSON.stringify({ items: [] }), { status: 200 });
    }
    if (url.endsWith('/missing')) {
      return new Response(JSON.stringify({ items: [] }), { status: 200 });
    }
    // adr0023-F1 BUG 1/4b regression test: InstanceFormDialog's detail
    // GET (/api/v1/admin/instances/<name>) needs real per-instance bodies
    // so the dialog actually populates url/type from the fetched detail
    // rather than falling through to the generic '{}' stub below.
    if (url.endsWith('/instances/homelab')) {
      return new Response(JSON.stringify({
        name: 'homelab', type: 'sonarr', url: 'http://sonarr:80', mode: 'auto',
      }), { status: 200 });
    }
    if (url.endsWith('/instances/films')) {
      return new Response(JSON.stringify({
        name: 'films', type: 'radarr', url: 'http://radarr:7878', mode: 'auto',
      }), { status: 200 });
    }
    if (url.endsWith('/webhook/status')) {
      return new Response(JSON.stringify({ installed: true }), { status: 200 });
    }
    if (url.endsWith('/qbit/settings')) {
      return new Response(JSON.stringify({ enabled: false }), { status: 200 });
    }
    return new Response('{}', { status: 200 });
  }) as never;
});

afterEach(() => { globalThis.fetch = origFetch; });

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

describe('<Instances />', () => {
  it('renders a rich card for every instance + ghost row', async () => {
    renderWithProviders(wrap(<Instances />));
    await waitFor(() => {
      expect(screen.getByTestId('instance-hero-homelab')).toBeInTheDocument();
    });
    expect(screen.getByTestId('instance-hero-4k')).toBeInTheDocument();
    expect(screen.getByTestId('instance-add-ghost')).toBeInTheDocument();
    // 4k is Unreachable with transitions_count: 3 → degraded card + flips badge.
    expect(screen.getByTestId('instance-hero-4k').className).toMatch(/border-l-status-danger/);
    expect(screen.getByTestId('hero-flips-4k')).toHaveTextContent('3');
  });

  it('Ф6-R-6b: renders a Radarr type badge for radarr rows and Sonarr for sonarr rows', async () => {
    renderWithProviders(wrap(<Instances />));
    await waitFor(() => {
      expect(screen.getByTestId('instance-hero-films')).toBeInTheDocument();
    });
    expect(screen.getByTestId('hero-type-films')).toHaveTextContent(
      i18n.t('instances.type.radarr'),
    );
    expect(screen.getByTestId('hero-type-homelab')).toHaveTextContent(
      i18n.t('instances.type.sonarr'),
    );
    // Sonarr-only widgets are guarded off for the radarr row.
    expect(screen.queryByTestId('hero-force-scan-films')).toBeNull();
  });

  it('shows empty state when zero instances', async () => {
    globalThis.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ instances: [] }), { status: 200 }),
    ) as never;
    renderWithProviders(wrap(<Instances />));
    await waitFor(() => {
      expect(screen.getByTestId('instances-empty-state')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('instance-add-ghost')).toBeNull();
  });

  it('sets the topbar page title via useSetPageTitle', async () => {
    const { getTitle } = renderPageWithTitle(<Instances />, { route: '/instances' });
    await waitFor(() => {
      expect(getTitle()).toBe(i18n.t('instances.title'));
    });
  });

  it('Story 494 / B-13: opens InstanceFormDialog in create mode when ?add=1 in URL', async () => {
    renderWithProviders(wrap(<Instances />), { route: '/instances?add=1' });
    // Dialog mounts and focus lands on the name input ("Имя"/"Name").
    const nameInput = await screen.findByLabelText(/имя|name/i);
    expect(nameInput).toBeInTheDocument();
    await waitFor(() => {
      expect(nameInput).toHaveFocus();
    });
  });

  it('adr0023-F1 BUG 1/4b: dirtying the edit form for one instance does not leak into a fresh open for another', async () => {
    const user = userEvent.setup();
    renderWithProviders(wrap(<Instances />));
    await waitFor(() => {
      expect(screen.getByTestId('instance-hero-homelab')).toBeInTheDocument();
    });

    // Open edit for "homelab", dirty the URL field, close WITHOUT saving.
    await user.click(
      within(screen.getByTestId('instance-hero-homelab'))
        .getByRole('button', { name: /изменить|edit/i }),
    );
    const urlInput = await screen.findByLabelText(/^url$/i) as HTMLInputElement;
    await waitFor(() => expect(urlInput.value).toBe('http://sonarr:80'));
    await user.clear(urlInput);
    await user.type(urlInput, 'http://DIRTY-LEAK:9999');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByLabelText(/^url$/i)).toBeNull());

    // Re-open edit for a DIFFERENT instance ("films", radarr).
    await user.click(
      within(screen.getByTestId('instance-hero-films'))
        .getByRole('button', { name: /изменить|edit/i }),
    );
    const urlInput2 = await screen.findByLabelText(/^url$/i) as HTMLInputElement;
    // Must show films' OWN url — never the dirty-leaked value, never
    // homelab's url, never a stale `type`.
    await waitFor(() => expect(urlInput2.value).toBe('http://radarr:7878'));
    expect(urlInput2.value).not.toBe('http://DIRTY-LEAK:9999');
  });
});
