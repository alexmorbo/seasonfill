// ADR-0009 S8 — Add-to-Sonarr modal pre-fill from per-instance defaults.
//
// The modal's real quality-profile / root-folder pickers are Radix Selects
// whose value display and option clicks are unreliable under JSDOM (portal +
// pointer events). To assert the *seeded* value and to drive an instance
// switch / manual override deterministically, this file mocks
// @/components/ui/select with a native <select> that forwards value,
// onValueChange (as onChange), disabled, id and data-testid, and flattens the
// SelectItem children into <option>s. The existing AddToSonarrModal.test.tsx
// keeps the real Radix widgets — this file is isolated so the mock does not
// leak into it.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { AddToSonarrModal } from './AddToSonarrModal';
import type { AddToSonarrTarget } from './add-to-sonarr-context';

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

// Native-<select> stand-in for @/components/ui/select. Flattens the composed
// Radix children into a plain controlled select so JSDOM can read .value and
// fireEvent.change can drive it.
vi.mock('@/components/ui/select', async () => {
  const React = (await import('react')).default;
  const tag = <F extends (...args: never[]) => unknown>(
    fn: F,
    name: string,
  ): F => {
    (fn as { __mockName?: string }).__mockName = name;
    return fn;
  };
  const SelectTrigger = tag(() => null, 'SelectTrigger');
  const SelectValue = tag(() => null, 'SelectValue');
  const SelectContent = tag(
    ({ children }: { children: unknown }) => children,
    'SelectContent',
  );
  const SelectItem = tag(() => null, 'SelectItem');

  type ItemProps = { value: string; disabled?: boolean; children: unknown };
  const Select = ({
    value,
    onValueChange,
    disabled,
    children,
  }: {
    value?: string;
    onValueChange?: (v: string) => void;
    disabled?: boolean;
    children: unknown;
  }) => {
    let triggerProps: Record<string, unknown> = {};
    const options: ItemProps[] = [];
    const walk = (node: unknown) => {
      React.Children.forEach(node, (child: unknown) => {
        if (!React.isValidElement(child)) return;
        const el = child as React.ReactElement<Record<string, unknown>> & {
          type: { __mockName?: string };
        };
        const kind = el.type?.__mockName;
        if (kind === 'SelectTrigger') {
          triggerProps = el.props;
        } else if (kind === 'SelectItem') {
          options.push(el.props as unknown as ItemProps);
        }
        if (el.props && el.props.children) walk(el.props.children);
      });
    };
    walk(children);
    return React.createElement(
      'select',
      {
        value: value ?? '',
        disabled: disabled || false,
        id: triggerProps.id,
        'data-testid': triggerProps['data-testid'],
        onChange: (e: { target: { value: string } }) =>
          onValueChange && onValueChange(e.target.value),
      },
      [
        React.createElement('option', { key: '__empty', value: '' }, ''),
        ...options.map((o) =>
          React.createElement(
            'option',
            { key: String(o.value), value: o.value, disabled: o.disabled },
            o.children as React.ReactNode,
          ),
        ),
      ],
    );
  };

  return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem };
});

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

const ME_PAYLOAD = {
  id: 1, username: 'alex', email: null, role: 'admin',
  auth_mode: 'forms', avatar_mode: 'auto', avatar_resolved_mode: 'monogram',
  avatar_hash: 'h', preferred_language: 'en-US',
  idp_profile_url: null, oidc_subject: null, last_login_at: null,
};
// Two quality profiles so a manual override to a *different* value is possible.
const QP_PAYLOAD = {
  items: [{ id: 6, name: 'HD-1080p' }, { id: 7, name: 'HD-720p' }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'main',
};
const RF_PAYLOAD = {
  items: [{ id: 9, path: '/tv', accessible: true, free_space: 100 }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'main',
};
// Root folder present but NOT accessible — exercises the accessible gate.
const RF_PAYLOAD_INACCESSIBLE = {
  items: [{ id: 9, path: '/tv', accessible: false, free_space: 100 }],
  refreshed_at: 'x', cache_status: 'hit', instance_name: 'main',
};
const LOOKUP_PAYLOAD = {
  items: [
    { season_number: 1, episode_count: 11, monitored: true },
  ],
  title: 'Rick and Morty', year: 2013, overview: 'x', image_url: '',
  tvdb_id: 275274, tmdb_id: 60625, instance_name: 'main',
};

type RouterOpts = {
  instances: unknown;
  rf?: unknown;
  qp?: unknown;
};

function makeRouter(opts: RouterOpts) {
  return (input: string | URL | Request): Response => {
    const url = typeof input === 'string' ? input : input.toString();
    if (url.endsWith('/api/v1/me')) {
      return json(ME_PAYLOAD);
    }
    if (url.endsWith('/api/v1/admin/instances')) {
      return json(opts.instances);
    }
    if (url.endsWith('/quality-profiles')) {
      return json(opts.qp ?? QP_PAYLOAD);
    }
    if (url.endsWith('/root-folders')) {
      return json(opts.rf ?? RF_PAYLOAD);
    }
    if (url.includes('/sonarr-lookup')) {
      return json(LOOKUP_PAYLOAD);
    }
    return json({});
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });
}

function renderModal() {
  const target: AddToSonarrTarget = {
    title: 'Rick and Morty', tvdbId: 81189, tmdbId: 1399,
  };
  const qc = mkClient();
  const onClose = vi.fn();
  const utils = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <AddToSonarrModal target={target} onClose={onClose} />
        </MemoryRouter>
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return { ...utils, qc, onClose };
}

function qpSelect(): HTMLSelectElement {
  return screen.getByTestId('add-to-sonarr-qp') as HTMLSelectElement;
}
function rfSelect(): HTMLSelectElement {
  return screen.getByTestId('add-to-sonarr-rf') as HTMLSelectElement;
}
function instanceSelect(): HTMLSelectElement {
  return screen.getByTestId('add-to-sonarr-instance') as HTMLSelectElement;
}

beforeEach(() => {
  fetchMock.mockReset();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/discover', assign: vi.fn() },
  });
  globalThis.fetch = fetchMock as typeof fetch;
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('<AddToSonarrModal /> S8 pre-fill', () => {
  // (a) instance defaults present in metadata → both selects pre-seeded.
  it('pre-selects the instance quality-profile + root-folder defaults', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [{
          name: 'main', health: 'Available', mode: 'auto',
          default_quality_profile_id: 6, default_root_folder_path: '/tv',
        }],
      },
    })(input as string));

    renderModal();

    await waitFor(() => expect(qpSelect().value).toBe('6'));
    await waitFor(() => expect(rfSelect().value).toBe('/tv'));
    // Both fields seeded → submit is enabled.
    expect(screen.getByTestId('add-to-sonarr-submit')).not.toBeDisabled();
  });

  // (b1) default quality-profile id absent from the fetched list → empty.
  it('leaves the profile empty when the default id is not in the list', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [{
          name: 'main', health: 'Available', mode: 'auto',
          default_quality_profile_id: 999, default_root_folder_path: '/tv',
        }],
      },
    })(input as string));

    renderModal();

    // Root seeds (present + accessible), profile stays empty (999 not in list).
    await waitFor(() => expect(rfSelect().value).toBe('/tv'));
    expect(qpSelect().value).toBe('');
    // Missing profile → submit blocked.
    expect(screen.getByTestId('add-to-sonarr-submit')).toBeDisabled();
  });

  // (b2) default root-folder present but NOT accessible → empty.
  it('leaves the root folder empty when the default path is not accessible', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [{
          name: 'main', health: 'Available', mode: 'auto',
          default_quality_profile_id: 6, default_root_folder_path: '/tv',
        }],
      },
      rf: RF_PAYLOAD_INACCESSIBLE,
    })(input as string));

    renderModal();

    await waitFor(() => expect(qpSelect().value).toBe('6'));
    expect(rfSelect().value).toBe('');
    expect(screen.getByTestId('add-to-sonarr-submit')).toBeDisabled();
  });

  // (c) switching instance re-seeds from the NEW instance's defaults.
  it('re-seeds when the instance is switched', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [
          {
            name: 'main', health: 'Available', mode: 'auto',
            default_quality_profile_id: 999, default_root_folder_path: '/nope',
          },
          {
            name: 'other', health: 'Available', mode: 'auto',
            default_quality_profile_id: 6, default_root_folder_path: '/tv',
          },
        ],
      },
    })(input as string));

    renderModal();

    // Wait for the instances list to load so the instance <select> exists.
    // Initial instance 'main' has no valid defaults → both empty.
    await screen.findByTestId('add-to-sonarr-instance');
    expect(qpSelect().value).toBe('');
    expect(rfSelect().value).toBe('');

    // Switch to 'other' (defaults present in the fresh lists).
    fireEvent.change(instanceSelect(), { target: { value: 'other' } });

    await waitFor(() => expect(qpSelect().value).toBe('6'));
    await waitFor(() => expect(rfSelect().value).toBe('/tv'));
  });

  // (d) a metadata refetch for the SAME instance does NOT clobber a manual pick.
  it('keeps a manual override across a metadata refetch', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [{
          name: 'main', health: 'Available', mode: 'auto',
          default_quality_profile_id: 6, default_root_folder_path: '/tv',
        }],
      },
    })(input as string));

    const { qc } = renderModal();

    // Seeded to the default first.
    await waitFor(() => expect(qpSelect().value).toBe('6'));

    // User overrides to a different profile.
    fireEvent.change(qpSelect(), { target: { value: '7' } });
    await waitFor(() => expect(qpSelect().value).toBe('7'));

    // A metadata refetch (same instance, same data) must NOT re-seed to '6'.
    await act(async () => {
      await qc.invalidateQueries({ queryKey: ['instance-metadata'] });
    });

    await waitFor(() => expect(qpSelect().value).toBe('7'));
    expect(qpSelect().value).toBe('7');
  });
});

describe('<AddToSonarrModal /> S3 instance seeding from target', () => {
  // (f) target.instanceName seeds the instance <select> to that instance,
  // overriding the "first instance" fallback.
  it('seeds the instance from target.instanceName', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [
          {
            name: 'main', health: 'Available', mode: 'auto',
            default_quality_profile_id: 6, default_root_folder_path: '/tv',
          },
          {
            name: 'other', health: 'Available', mode: 'auto',
            default_quality_profile_id: 6, default_root_folder_path: '/tv',
          },
        ],
      },
    })(input as string));

    const target: AddToSonarrTarget = {
      title: 'Rick and Morty', tvdbId: 81189, instanceName: 'other',
    };
    const qc = mkClient();
    render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <MemoryRouter>
            <AddToSonarrModal target={target} onClose={vi.fn()} />
          </MemoryRouter>
        </QueryClientProvider>
      </I18nextProvider>,
    );

    await waitFor(() => expect(instanceSelect().value).toBe('other'));
  });
});

describe('<AddToSonarrModal /> S2 search-on-add + portable seasons', () => {
  function seasonCheckbox(n: number): HTMLElement {
    return within(screen.getByTestId(`add-to-sonarr-season-${n}`))
      .getByRole('checkbox');
  }
  function searchToggle(): HTMLElement {
    return within(screen.getByTestId('add-to-sonarr-search-on-add'))
      .getByRole('checkbox');
  }
  function addCallBody(): Record<string, unknown> {
    const call = fetchMock.mock.calls.find(([u]) =>
      String(u).endsWith('/discovery/add-to-sonarr'));
    expect(call).toBeTruthy();
    return JSON.parse((call![1] as RequestInit).body as string);
  }

  // (1) Instance switch keeps season selection — season numbers are stable, so
  // an unchecked season stays unchecked after switching to another instance.
  it('preserves the season selection across an instance switch', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [
          {
            name: 'main', health: 'Available', mode: 'auto',
            default_quality_profile_id: 6, default_root_folder_path: '/tv',
          },
          {
            name: 'other', health: 'Available', mode: 'auto',
            default_quality_profile_id: 6, default_root_folder_path: '/tv',
          },
        ],
      },
    })(input as string));

    renderModal();

    // Season 1 is on by default; wait for the lookup-derived checkbox.
    await screen.findByTestId('add-to-sonarr-season-1');
    await waitFor(() =>
      expect(seasonCheckbox(1).getAttribute('data-state')).toBe('checked'));

    // Uncheck it.
    fireEvent.click(seasonCheckbox(1));
    await waitFor(() =>
      expect(seasonCheckbox(1).getAttribute('data-state')).toBe('unchecked'));

    // Switch instance — the season choice must survive the switch.
    fireEvent.change(instanceSelect(), { target: { value: 'other' } });

    await screen.findByTestId('add-to-sonarr-season-1');
    await waitFor(() =>
      expect(seasonCheckbox(1).getAttribute('data-state')).toBe('unchecked'));
  });

  // (2)+(3) Toggling search-on-add sends search_on_add:true and NO monitor_mode.
  it('submits search_on_add=true and no monitor_mode when toggled', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [{
          name: 'main', health: 'Available', mode: 'auto',
          default_quality_profile_id: 6, default_root_folder_path: '/tv',
        }],
      },
    })(input as string));

    renderModal();

    await waitFor(() => expect(qpSelect().value).toBe('6'));
    await waitFor(() => expect(rfSelect().value).toBe('/tv'));

    fireEvent.click(searchToggle());
    await waitFor(() =>
      expect(searchToggle().getAttribute('data-state')).toBe('checked'));

    fireEvent.click(screen.getByTestId('add-to-sonarr-submit'));

    let body: Record<string, unknown> = {};
    await waitFor(() => { body = addCallBody(); });
    expect(body.search_on_add).toBe(true);
    expect('monitor_mode' in body).toBe(false);
  });

  // (4) Default submit (untouched toggle) sends search_on_add:false, no mode.
  it('submits search_on_add=false by default with no monitor_mode', async () => {
    fetchMock.mockImplementation(async (input) => makeRouter({
      instances: {
        instances: [{
          name: 'main', health: 'Available', mode: 'auto',
          default_quality_profile_id: 6, default_root_folder_path: '/tv',
        }],
      },
    })(input as string));

    renderModal();

    await waitFor(() => expect(qpSelect().value).toBe('6'));
    await waitFor(() => expect(rfSelect().value).toBe('/tv'));

    fireEvent.click(screen.getByTestId('add-to-sonarr-submit'));

    let body: Record<string, unknown> = {};
    await waitFor(() => { body = addCallBody(); });
    expect(body.search_on_add).toBe(false);
    expect('monitor_mode' in body).toBe(false);
  });
});
