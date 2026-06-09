import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { IntegrationsTab } from '../IntegrationsTab';

const origFetch = globalThis.fetch;

interface RuntimeConfigBody {
  cron: object;
  scan: object;
  dry_run: boolean;
  global_rate_limit: object;
  auth: object;
  auto_generated_api_key: boolean;
  updated_at: string;
  guid_rewrites: Array<{ from: string; to: string }>;
}

let lastPut: { url: string; body: RuntimeConfigBody } | null = null;
let runtimeConfig: RuntimeConfigBody = {
  cron: {},
  scan: {},
  dry_run: false,
  global_rate_limit: {},
  auth: {},
  auto_generated_api_key: false,
  updated_at: new Date().toISOString(),
  guid_rewrites: [],
};

function makeFetch() {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/v1/webhooks/status')) {
      return new Response(JSON.stringify({
        items: [], healthy_count: 0, unhealthy_count: 0,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/v1/config/runtime')) {
      if (init?.method === 'PUT') {
        const parsed = JSON.parse(String(init.body)) as RuntimeConfigBody;
        lastPut = { url, body: parsed };
        runtimeConfig = { ...runtimeConfig, ...parsed };
        return new Response(JSON.stringify(runtimeConfig), {
          status: 200, headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(runtimeConfig), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;
}

beforeEach(() => {
  lastPut = null;
  runtimeConfig = {
    cron: {}, scan: {}, dry_run: false, global_rate_limit: {}, auth: {},
    auto_generated_api_key: false, updated_at: new Date().toISOString(),
    guid_rewrites: [],
  };
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<IntegrationsTab /> · guid_rewrites editor', () => {
  it('renders the section header and the empty-state copy when no rules are configured', async () => {
    globalThis.fetch = makeFetch();
    renderWithProviders(<IntegrationsTab />);
    // Section header in Russian or English depending on detected locale.
    await screen.findByRole('heading', { name: /Tracker link rewrites|Замены трекер-ссылок/i });
    await waitFor(() => {
      // Empty-state hint.
      expect(
        screen.getByText(/No rewrites configured|Замены не настроены/i),
      ).toBeInTheDocument();
    });
  });

  it('seeds the editor from runtime config and supports add + edit + save', async () => {
    runtimeConfig.guid_rewrites = [
      { from: 'http://existing-proxy', to: 'https://existing.example.com' },
    ];
    globalThis.fetch = makeFetch();
    renderWithProviders(<IntegrationsTab />);

    // Wait for the seeded row to render.
    const seededFrom = await screen.findByTestId('guid-rewrite-from-0');
    expect(seededFrom).toHaveValue('http://existing-proxy');

    // Add a second row and fill it in.
    await userEvent.click(screen.getByTestId('guid-rewrites-add'));
    const fromInput = await screen.findByTestId('guid-rewrite-from-1');
    const toInput = screen.getByTestId('guid-rewrite-to-1');
    await userEvent.type(
      fromInput,
      'http://rutracker-proxy.servarr.svc.cluster.local',
    );
    await userEvent.type(toInput, 'https://rutracker.org');

    // Save and assert the PUT body carries both rules in order.
    await userEvent.click(screen.getByTestId('guid-rewrites-save'));
    await waitFor(() => {
      expect(lastPut).not.toBeNull();
    });
    const body = lastPut?.body;
    expect(body?.guid_rewrites).toHaveLength(2);
    expect(body?.guid_rewrites[0]).toEqual({
      from: 'http://existing-proxy',
      to: 'https://existing.example.com',
    });
    expect(body?.guid_rewrites[1]).toEqual({
      from: 'http://rutracker-proxy.servarr.svc.cluster.local',
      to: 'https://rutracker.org',
    });
  });

  it('drops rows whose `from` is blank on save (mid-edit drafts do not 400)', async () => {
    globalThis.fetch = makeFetch();
    renderWithProviders(<IntegrationsTab />);

    await screen.findByRole('heading', { name: /Tracker link rewrites|Замены трекер-ссылок/i });

    // Add two rows: one valid, one with only `to` filled in.
    await userEvent.click(screen.getByTestId('guid-rewrites-add'));
    await userEvent.click(screen.getByTestId('guid-rewrites-add'));

    await userEvent.type(screen.getByTestId('guid-rewrite-from-0'), 'http://a');
    await userEvent.type(screen.getByTestId('guid-rewrite-to-0'), 'https://a');
    // Row 1: leave `from` blank, fill only `to`.
    await userEvent.type(screen.getByTestId('guid-rewrite-to-1'), 'https://ignored');

    await userEvent.click(screen.getByTestId('guid-rewrites-save'));
    await waitFor(() => {
      expect(lastPut).not.toBeNull();
    });
    expect(lastPut?.body.guid_rewrites).toHaveLength(1);
    expect(lastPut?.body.guid_rewrites[0]).toEqual({
      from: 'http://a',
      to: 'https://a',
    });
  });

  it('removes a row when the X button is clicked', async () => {
    runtimeConfig.guid_rewrites = [
      { from: 'http://a', to: 'https://a' },
      { from: 'http://b', to: 'https://b' },
    ];
    globalThis.fetch = makeFetch();
    renderWithProviders(<IntegrationsTab />);

    await screen.findByTestId('guid-rewrite-from-0');
    await userEvent.click(screen.getByTestId('guid-rewrite-remove-0'));

    // Row 0 should now carry what was previously row 1.
    await waitFor(() => {
      expect(screen.getByTestId('guid-rewrite-from-0')).toHaveValue('http://b');
    });
    expect(screen.queryByTestId('guid-rewrite-from-1')).not.toBeInTheDocument();
  });
});
