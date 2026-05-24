import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { QueryClient } from '@tanstack/react-query';
import { renderWithProviders } from '@/test-utils';
import { InstanceFormDialog } from './InstanceFormDialog';
import {
  instanceDetailKey,
  type InstanceDetail,
  type InstanceDetailWithMeta,
} from '@/lib/instances-mutations';
import { DtoInstanceDetailMode } from '@/api/schema';

const origFetch = globalThis.fetch;
beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/settings', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

// seedDetail pre-populates the react-query cache with an
// InstanceDetailWithMeta entry so useInstanceDetail resolves
// synchronously, mirroring the real flow where the parent
// InstancesTab has already fetched the detail.
function seedDetail(qc: QueryClient, name: string, detail: InstanceDetail): void {
  const entry: InstanceDetailWithMeta = {
    detail,
    lastModified: 'Mon, 25 May 2026 12:00:00 GMT',
  };
  qc.setQueryData(instanceDetailKey(name), entry);
}

describe('<InstanceFormDialog />', () => {
  it('name input is disabled in edit mode', async () => {
    const { qc } = renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }}
      />,
    );
    seedDetail(qc, 'alpha', { name: 'alpha', url: 'http://x', mode: DtoInstanceDetailMode.auto });
    const nameInput = await screen.findByLabelText(/name/i);
    expect(nameInput).toBeDisabled();
  });

  it('shows the encrypted-at-rest badge', async () => {
    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    expect(await screen.findByText(/encrypted at rest/i)).toBeVisible();
  });

  it('clicking Test connection calls /instances/test with the form values', async () => {
    const captured: { url?: string; body?: string } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ ok: true, version: '4.0.0.999' }, 200);
    }) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    await userEvent.click(screen.getByRole('button', { name: /test connection/i }));
    await waitFor(() => {
      expect(captured.url).toBe('/api/v1/instances/test');
    });
    expect(JSON.parse(captured.body ?? '{}')).toEqual({
      url: 'http://sonarr:8989',
      api_key: 'sekrit',
    });
    expect(await screen.findByText(/OK — Sonarr 4\.0\.0\.999/i)).toBeVisible();
  });

  it('edit submit with blank api_key OMITS the field from the PUT body', async () => {
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    const minDetail = { name: 'alpha', url: 'http://x', mode: DtoInstanceDetailMode.auto };
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && !init?.method) {
        // background GET — return the detail so stale refetch succeeds
        return jsonResp(minDetail, 200);
      }
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }}
      />,
    );
    seedDetail(qc, 'alpha', minDetail);

    // Wait for the Save button to become enabled (detail loaded from cache).
    const saveBtn = await screen.findByRole('button', { name: /^save$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());
    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.method).toBe('PUT'));
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent).not.toHaveProperty('api_key');
  });

  it('edit preserves non-form per-instance fields (cooldown / ranking / limits)', async () => {
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    // fullDetail is defined below; fetch mock references it via closure after assignment.
    let fullDetailRef: InstanceDetail | null = null;
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        // background GET — return full detail so stale refetch doesn't clobber cache
        return new Response(
          JSON.stringify(fullDetailRef ?? { name: 'alpha' }),
          { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
        );
      }
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const fullDetail: InstanceDetail = {
      name: 'alpha',
      url: 'http://x',
      mode: DtoInstanceDetailMode.auto,
      api_key: '***',
      timeout_sec: 17,
      search_timeout_sec: 91,
      dry_run: true,
      tags: { mode: 'include', include: ['tv'], exclude: [] },
      search: {
        require_all_aired: true,
        skip_specials: false,
        skip_anime: true,
        min_custom_format_score: 15,
      },
      ranking: { indexer_priority_enabled: true, origin_bonus: 1.5 },
      limits: { scan_max_series: 42, max_grabs_per_scan: 7 },
      rate_limit_rpm: 5,
      rate_limit_burst: 2,
      cooldown: {
        mode: 'smart',
        series_after_grab_sec: 14400,
        guid_after_failed_grab_sec: 1800,
        guid_after_failed_import_sec: 3600,
      },
      retry: { max_attempts: 5, initial_backoff_sec: 2, max_backoff_sec: 30 },
      health_check: { recheck_auth_sec: 600, recheck_network_sec: 60 },
      updated_at: '2026-05-25T12:00:00Z',
    } as InstanceDetail;
    fullDetailRef = fullDetail;

    const { qc } = renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }}
      />,
    );
    seedDetail(qc, 'alpha', fullDetail);

    // Edit a form field (URL) so we can prove ONLY form fields are
    // overlaid; everything else round-trips verbatim.
    const urlInput = await screen.findByLabelText(/url/i);
    await userEvent.clear(urlInput);
    await userEvent.type(urlInput, 'http://sonarr.changed:8989');

    const saveBtn = screen.getByRole('button', { name: /^save$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());
    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.method).toBe('PUT'));

    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    // Form fields overlaid:
    expect(sent.url).toBe('http://sonarr.changed:8989');
    expect(sent.name).toBe('alpha');
    expect(sent.mode).toBe('auto');
    // updated_at stripped (not echoed back to server):
    expect(sent).not.toHaveProperty('updated_at');
    // Non-form fields preserved verbatim:
    expect(sent.timeout_sec).toBe(17);
    expect(sent.search_timeout_sec).toBe(91);
    expect(sent.dry_run).toBe(true);
    expect(sent.rate_limit_rpm).toBe(5);
    expect(sent.rate_limit_burst).toBe(2);
    expect(sent.tags).toEqual({ mode: 'include', include: ['tv'], exclude: [] });
    expect(sent.search).toEqual({
      require_all_aired: true,
      skip_specials: false,
      skip_anime: true,
      min_custom_format_score: 15,
    });
    expect(sent.ranking).toEqual({ indexer_priority_enabled: true, origin_bonus: 1.5 });
    expect(sent.limits).toEqual({ scan_max_series: 42, max_grabs_per_scan: 7 });
    expect(sent.cooldown).toEqual({
      mode: 'smart',
      series_after_grab_sec: 14400,
      guid_after_failed_grab_sec: 1800,
      guid_after_failed_import_sec: 3600,
    });
    expect(sent.retry).toEqual({ max_attempts: 5, initial_backoff_sec: 2, max_backoff_sec: 30 });
    expect(sent.health_check).toEqual({ recheck_auth_sec: 600, recheck_network_sec: 60 });
  });

  it('edit Save is disabled until detail is loaded', async () => {
    // No seed → useInstanceDetail stays pending (fetch returns
    // never-resolving promise so the query hangs in `pending`).
    globalThis.fetch = vi.fn(() => new Promise(() => {})) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }}
      />,
    );
    const save = await screen.findByRole('button', { name: /^save$/i });
    expect(save).toBeDisabled();
    expect(await screen.findByText(/loading instance details/i)).toBeVisible();
  });
});
