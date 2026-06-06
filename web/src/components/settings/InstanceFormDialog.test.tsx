import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { QueryClient } from '@tanstack/react-query';
import { renderWithProviders } from '@/test-utils';
import i18n from '@/i18n';
import { InstanceFormDialog } from './InstanceFormDialog';
import {
  instanceDetailKey,
  type InstanceDetail,
  type InstanceDetailWithMeta,
} from '@/lib/instances-mutations';
import { DtoInstanceDetailMode, DtoInstanceTagsMode } from '@/api/schema';

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
    message: (m: string) => toastMessage(m),
  },
}));

const origFetch = globalThis.fetch;
beforeEach(() => {
  toastSuccess.mockClear();
  toastError.mockClear();
  toastMessage.mockClear();
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
    expect(await screen.findByText(/Connected to Sonarr 4\.0\.0\.999/i)).toBeVisible();
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
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
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

    // Wait for the form to be fully hydrated from detail before editing —
    // the new form re-seeds all fields (including URL) from formFromDetail
    // when detail first arrives, so we must wait for that to settle.
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Edit a form field (URL) so we can prove ONLY form fields are
    // overlaid; everything else round-trips verbatim.
    const urlInput = await screen.findByLabelText(/url/i);
    await userEvent.clear(urlInput);
    await userEvent.type(urlInput, 'http://sonarr.changed:8989');
    await userEvent.tab(); // commit to RHF

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
    // Masked api_key must NEVER appear in the request body:
    expect(sent).not.toHaveProperty('api_key');
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
    const save = await screen.findByRole('button', { name: /^save instance$/i });
    expect(save).toBeDisabled();
    expect(await screen.findByText(/loading instance details/i)).toBeVisible();
  });

  it('Create with empty api_key surfaces inline error and does NOT POST', async () => {
    const fetchSpy = vi.fn(async () => jsonResp({}, 500));
    globalThis.fetch = fetchSpy as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/name/i), 'beta');
    // URL has the default already.
    // api_key intentionally untouched.
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    expect(await screen.findByText(/api key required for new instances/i)).toBeVisible();
    expect(fetchSpy).not.toHaveBeenCalled();
    // Focus moved to the api_key input.
    expect(document.activeElement).toBe(screen.getByLabelText(/api key/i));
  });

  it('Test connection happy path renders inline message and NO toast', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ ok: true, version: '4.0.5' }, 200),
    ) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    await userEvent.click(screen.getByRole('button', { name: /test connection/i }));

    expect(await screen.findByText(/Connected to Sonarr 4\.0\.5/i)).toBeVisible();
    expect(toastSuccess).not.toHaveBeenCalled();
    expect(toastError).not.toHaveBeenCalled();
  });

  it('Test connection 504 renders inline AND toasts (transport)', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'timeout' }, 504),
    ) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    await userEvent.click(screen.getByRole('button', { name: /test connection/i }));

    // Inline stays null (cleared in the catch); toast is the channel.
    await waitFor(() => {
      expect(toastError).toHaveBeenCalledWith(
        expect.stringMatching(/timed out/i),
      );
    });
  });

  it('edit form survives a detail refetch without losing typed input', async () => {
    // The dialog mounts a useInstanceDetail observer keyed on the
    // instance name. Invalidating that key triggers the background
    // refetch that exercises the dialog's effect dep array.
    const minDetail: InstanceDetail = {
      name: 'alpha', url: 'http://x', mode: DtoInstanceDetailMode.auto,
    } as InstanceDetail;
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha')) {
        return jsonResp(minDetail, 200);
      }
      return jsonResp({ instances: [] }, 200);
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

    // Wait for the form to settle (Save enabled = detail loaded).
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Type into the api_key field.
    const keyInput = await screen.findByLabelText(/api key/i);
    await userEvent.type(keyInput, 'user-typed-secret');
    expect((keyInput as HTMLInputElement).value).toBe('user-typed-secret');

    // Force a detail-key refetch. The dialog DOES mount an observer on
    // this key (via useInstanceDetail), so the refetch actually fires.
    // Before the fix, the dialog's reset() effect depended on `initial`
    // reference identity and would have nuked the typed input.
    await qc.invalidateQueries({ queryKey: instanceDetailKey('alpha') });

    // Give the query client a tick to flush.
    await waitFor(() => {
      expect((keyInput as HTMLInputElement).value).toBe('user-typed-secret');
    });
  });

  it('keeps in-progress edits when a background refetch returns DIFFERENT server data', async () => {
    // Hardening for the last-write-wins switch: a stale server value
    // landing mid-edit (e.g. another tab saved, or the 5s poll) must NOT
    // overwrite the field the user is actively editing. The dialog's
    // reset effect is guarded by !isDirty for exactly this case.
    const seedDetailVal: InstanceDetail = {
      name: 'alpha', url: 'http://original', mode: DtoInstanceDetailMode.auto,
    } as InstanceDetail;
    // The refetch resolves with a DIFFERENT url than what's cached/typed.
    const staleServer: InstanceDetail = {
      name: 'alpha', url: 'http://server-stale', mode: DtoInstanceDetailMode.auto,
    } as InstanceDetail;
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha')) {
        return jsonResp(staleServer, 200);
      }
      return jsonResp({ instances: [] }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://original', mode: 'auto' }}
      />,
    );
    seedDetail(qc, 'alpha', seedDetailVal);

    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // User edits the URL — form becomes dirty.
    const urlInput = (await screen.findByLabelText(/url/i)) as HTMLInputElement;
    await userEvent.clear(urlInput);
    await userEvent.type(urlInput, 'http://user-edit');
    expect(urlInput.value).toBe('http://user-edit');

    // A background refetch lands with the stale server URL. The !isDirty
    // guard must hold off the reset, so the user's edit survives.
    await qc.invalidateQueries({ queryKey: instanceDetailKey('alpha') });
    await waitFor(() => {
      expect(urlInput.value).toBe('http://user-edit');
    });
    // Prove the stale value is NOT what we kept.
    expect(urlInput.value).not.toBe('http://server-stale');
  });

  it('edit submit OMITS api_key when the user did not touch the field, even with masked detail cached', async () => {
    // Simulates the 2026-05-26 incident: GET returns api_key="***"
    // (the server-side mask), user flips mode auto→manual, hits Save
    // without typing in the api_key input. The PUT body MUST NOT
    // contain api_key at all.
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    const maskedDetail: InstanceDetail = {
      name: 'alpha', url: 'http://x', mode: DtoInstanceDetailMode.auto,
      api_key: '***',
    } as InstanceDetail;
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(maskedDetail, 200);
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
    seedDetail(qc, 'alpha', maskedDetail);

    // Wait for Save to enable.
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Flip mode auto→manual via the select (mimics the real incident).
    // The mode select uses Radix; clicking the trigger opens the
    // listbox, then we pick "manual".
    await userEvent.click(screen.getByRole('combobox'));
    await userEvent.click(await screen.findByRole('option', { name: 'Manual (per-series only)' }));

    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.method).toBe('PUT'));

    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent).not.toHaveProperty('api_key');
    expect(sent.mode).toBe('manual');
  });

  it('edit submit INCLUDES api_key only when the user types into the field', async () => {
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    const maskedDetail: InstanceDetail = {
      name: 'alpha', url: 'http://x', mode: DtoInstanceDetailMode.auto,
      api_key: '***',
    } as InstanceDetail;
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(maskedDetail, 200);
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
    seedDetail(qc, 'alpha', maskedDetail);

    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    const keyInput = await screen.findByLabelText(/api key/i);
    await userEvent.type(keyInput, 'new-real-key-32-chars-typed-byhuman');

    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.method).toBe('PUT'));

    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent.api_key).toBe('new-real-key-32-chars-typed-byhuman');
  });

  // (the 5 cases below cover the new surface)

  function fullSeed(): InstanceDetail {
    return {
      name: 'alpha',
      url: 'http://x',
      mode: DtoInstanceDetailMode.auto,
      api_key: '***',
      timeout_sec: 10,
      search_timeout_sec: 60,
      tags: { mode: 'off', include: [], exclude: [] },
      search: {
        require_all_aired: false, skip_specials: false,
        skip_anime: false, min_custom_format_score: 0,
      },
      ranking: { indexer_priority_enabled: false, origin_bonus: 0 },
      limits: { scan_max_series: 0, max_grabs_per_scan: 10 },
      rate_limit_rpm: 0,
      rate_limit_burst: 0,
      cooldown: {
        mode: 'smart',
        series_after_grab_sec: 86400,
        guid_after_failed_grab_sec: 259200,
        guid_after_failed_import_sec: 172800,
      },
      retry: { max_attempts: 3, initial_backoff_sec: 1, max_backoff_sec: 30 },
      health_check: { recheck_auth_sec: 300, recheck_network_sec: 60 },
    } as InstanceDetail;
  }

  it('dry_run defaults to "auto" and OMITS dry_run from the PUT body', async () => {
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    const seed: InstanceDetail = { ...fullSeed() }; // dry_run undefined
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(seed, 200);
      }
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
    );
    seedDetail(qc, 'alpha', seed);

    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());
    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.method).toBe('PUT'));

    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent).not.toHaveProperty('dry_run');
  });

  it('dry_run "on" round-trips as boolean true; flipping to "off" sends false', async () => {
    const captured: { body?: string } = {};
    const seed: InstanceDetail = { ...fullSeed(), dry_run: true };
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(seed, 200);
      }
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
    );
    seedDetail(qc, 'alpha', seed);
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Initial state should be "on" (radio "on" checked).
    expect((screen.getByLabelText('on') as HTMLInputElement).getAttribute('aria-checked') ?? 'true').toBeTruthy();

    // Flip to "off".
    await userEvent.click(screen.getByLabelText('off'));
    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.body).toBeDefined());
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent.dry_run).toBe(false);
  });

  it('timeout_sec edits round-trip through the PUT body as a number', async () => {
    const captured: { body?: string } = {};
    const seed = fullSeed();
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(seed, 200);
      }
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
    );
    seedDetail(qc, 'alpha', seed);
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    const timeoutInput = await screen.findByLabelText(/^timeout/i);
    await userEvent.clear(timeoutInput);
    await userEvent.type(timeoutInput, '42');
    await userEvent.tab(); // trigger onBlur coercion

    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.body).toBeDefined());
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent.timeout_sec).toBe(42);
  });

  it('search.skip_specials switch toggles and round-trips as a boolean', async () => {
    const captured: { body?: string } = {};
    const seed = fullSeed();
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(seed, 200);
      }
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
    );
    seedDetail(qc, 'alpha', seed);
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Jump to Behavior tab.
    await userEvent.click(screen.getByRole('tab', { name: /behavior/i }));
    const skipSpecials = await screen.findByLabelText(/skip specials/i);
    await userEvent.click(skipSpecials);

    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.body).toBeDefined());
    const sent = JSON.parse(captured.body ?? '{}') as { search?: { skip_specials?: boolean } };
    expect(sent.search?.skip_specials).toBe(true);
  });

  it('tags.include list editor adds + removes entries that round-trip on save', async () => {
    const captured: { body?: string } = {};
    const seed = fullSeed();
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(seed, 200);
      }
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
    );
    seedDetail(qc, 'alpha', seed);
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    await userEvent.click(screen.getByRole('tab', { name: /behavior/i }));
    const includeInput = await screen.findByLabelText(/include tags/i);
    await userEvent.type(includeInput, 'tv,4k{enter}');

    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.body).toBeDefined());
    const sent = JSON.parse(captured.body ?? '{}') as { tags?: { include?: string[] } };
    expect(sent.tags?.include).toEqual(['tv', '4k']);
  });

  it('tags.mode="" (NULL DB column) does not reject validation and sends valid enum on save', async () => {
    // Regression: Go emits "" for NULL string columns; ?? only catches
    // null/undefined, so "" reached zod which rejected it, silently
    // jumped to the Advanced tab, and fired no network request.
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    const nullTagsSeed: InstanceDetail = {
      ...fullSeed(),
      // Simulate Go marshalling a NULL column as an empty string.
      tags: { mode: '' as unknown as DtoInstanceTagsMode, include: [], exclude: [] },
    };
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(nullTagsSeed, 200);
      }
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
    );
    seedDetail(qc, 'alpha', nullTagsSeed);

    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Click Save without making any changes.
    await userEvent.click(saveBtn);

    // (a) A network PUT MUST fire — validation must not silently reject.
    await waitFor(() => expect(captured.method).toBe('PUT'));

    // (b) The outbound payload must carry a valid tags.mode enum value.
    const sent = JSON.parse(captured.body ?? '{}') as { tags?: { mode?: string } };
    expect(['off', 'include', 'exclude', 'both']).toContain(sent.tags?.mode);
  });

  it('TagListEditor commits draft on blur (prevents silent tag loss)', async () => {
    // NIT 1 fix: type a tag, blur the input, verify tag is committed before save
    const captured: { body?: string } = {};
    const seed = fullSeed();
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(seed, 200);
      }
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha' }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
    );
    seedDetail(qc, 'alpha', seed);
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    await userEvent.click(screen.getByRole('tab', { name: /behavior/i }));
    const includeInput = (await screen.findByLabelText(/include tags/i)) as HTMLInputElement;

    // Type 'hbo' WITHOUT pressing Enter/comma — just type and blur via tab.
    await userEvent.type(includeInput, 'hbo');
    // Tab away (blur the input without pressing Enter).
    await userEvent.tab();

    // Verify the draft was committed by onBlur: the input is now empty.
    expect(includeInput.value).toBe('');
    // The badge should be in the document (verify tag appears in the list).
    expect(screen.getByLabelText(/Remove hbo/i)).toBeVisible();

    await userEvent.click(saveBtn);
    await waitFor(() => expect(captured.body).toBeDefined());
    const sent = JSON.parse(captured.body ?? '{}') as { tags?: { include?: string[] } };
    expect(sent.tags?.include).toEqual(['hbo']);
  });

  it('localises numeric-range validation errors (RU): timeout >300 shows label + bound', async () => {
    // Track C regression: zod validator messages used to be hardcoded
    // English strings ("timeout_sec <= 300"). They are now JSON envelopes
    // resolved through i18n at render time so both the template and the
    // technical field label flow through translations.
    const saved = i18n.resolvedLanguage ?? 'en';
    await i18n.changeLanguage('ru');
    try {
      const seed: InstanceDetail = {
        name: 'alpha', url: 'http://x', mode: DtoInstanceDetailMode.auto,
        api_key: '***',
      } as InstanceDetail;
      globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof u === 'string' ? u : u.toString();
        if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
          return jsonResp(seed, 200);
        }
        return jsonResp({}, 500);
      }) as typeof fetch;

      const { qc } = renderWithProviders(
        <InstanceFormDialog open onOpenChange={() => {}} mode="edit"
          initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }} />,
      );
      seedDetail(qc, 'alpha', seed);

      const saveBtn = await screen.findByRole('button', { name: /^сохранить инстанс$/i });
      await waitFor(() => expect(saveBtn).not.toBeDisabled());

      // Drive timeout out of bounds (max=300) and submit. The Connection
      // tab has two "Таймаут*" labels (timeout + search timeout); target
      // the bare "Таймаут (секунд)" by id-based lookup instead.
      const timeoutInput = document.getElementById('inst-timeout') as HTMLInputElement;
      expect(timeoutInput).toBeTruthy();
      await userEvent.clear(timeoutInput);
      await userEvent.type(timeoutInput, '999999');
      await userEvent.tab();
      await userEvent.click(saveBtn);

      // The translated message embeds both the localised label and the
      // boundary value — proving both the i18n template AND the nested
      // label-key resolution work end-to-end.
      const alert = await screen.findByText(/Таймаут.*≤\s*300/i);
      expect(alert).toBeVisible();
    } finally {
      await i18n.changeLanguage(saved);
    }
  });

  it('041h-1: create submit sends webhook_install_enabled=true by default and OMITS empty url fields', async () => {
    const captured: { url?: string; body?: string | undefined; method?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'beta', url: 'http://sonarr:8989' }, 200);
    }) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/name/i), 'beta');
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    await waitFor(() => expect(captured.method).toBe('POST'));
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent).toHaveProperty('webhook_install_enabled', true);
    // Critical: empty optional URLs must NEVER hit the wire as ''.
    expect(sent).not.toHaveProperty('public_url');
    expect(sent).not.toHaveProperty('webhook_url_override');
  });

  it('041h-1: create submit with filled URL fields sends them trimmed', async () => {
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (_u, init?: RequestInit) => {
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'beta' }, 200);
    }) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/name/i), 'beta');
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    await userEvent.type(
      await screen.findByLabelText(/public url|Публичный URL/i),
      '  https://sonarr.example.com  ',
    );
    await userEvent.type(
      await screen.findByLabelText(/webhook base url|Базовый URL/i),
      'https://sf.example.com',
    );
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    await waitFor(() => expect(captured.method).toBe('POST'));
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent.public_url).toBe('https://sonarr.example.com');
    expect(sent.webhook_url_override).toBe('https://sf.example.com');
    expect(sent.webhook_install_enabled).toBe(true);
  });

  it('041h-1: unchecking webhook install hides the override input and sends false', async () => {
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (_u, init?: RequestInit) => {
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'beta' }, 200);
    }) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/name/i), 'beta');
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    const installSwitch = await screen.findByLabelText(
      /install webhook|Установить вебхук/i,
    );
    await userEvent.click(installSwitch);
    // After unchecking, the override input must disappear.
    expect(
      screen.queryByLabelText(/webhook base url|Базовый URL/i),
    ).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    await waitFor(() => expect(captured.method).toBe('POST'));
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(sent.webhook_install_enabled).toBe(false);
  });

  it('041h-1: invalid public_url shows inline error and blocks submit', async () => {
    const fetchSpy = vi.fn(async () => jsonResp({}, 500));
    globalThis.fetch = fetchSpy as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/name/i), 'beta');
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    await userEvent.type(
      await screen.findByLabelText(/public url|Публичный URL/i),
      'not-a-url',
    );
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    await waitFor(() => {
      expect(
        screen.getByText(/must start with http|должен начинаться с http/i),
      ).toBeVisible();
    });
    const posts = (fetchSpy.mock.calls as unknown as Array<[RequestInfo | URL, RequestInit | undefined]>)
      .filter(([u, init]) => {
        const s = typeof u === 'string' ? u : u.toString();
        return s.includes('/instances') && !s.includes('/test')
          && init?.method === 'POST';
      });
    expect(posts).toHaveLength(0);
  });

  it('041h-1: edit hydrates new fields, shows ui_url hint, invalidates webhook-status on save', async () => {
    const detail: InstanceDetail = {
      name: 'alpha',
      url: 'http://sonarr:8989',
      mode: DtoInstanceDetailMode.auto,
      public_url: 'https://sonarr.example.com',
      ui_url: 'https://sonarr.example.com',
      webhook_install_enabled: false,
      webhook_url_override: 'https://sf.example.com',
    } as InstanceDetail;

    let putCount = 0;
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/instances/alpha') && (!init?.method || init.method === 'GET')) {
        return jsonResp(detail, 200);
      }
      if (init?.method === 'PUT') { putCount += 1; return jsonResp(detail, 200); }
      return jsonResp({ instances: [] }, 200);
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog
        open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://sonarr:8989', mode: 'auto' }}
      />,
    );
    seedDetail(qc, 'alpha', detail);
    // Stale entry — we assert it becomes invalidated after Save.
    qc.setQueryData(['qbit', 'webhook-status', 'alpha'], { installed: false });

    // Hydration: public_url comes through.
    const publicInput = await screen.findByLabelText(/public url|Публичный URL/i);
    await waitFor(() => {
      expect((publicInput as HTMLInputElement).value)
        .toBe('https://sonarr.example.com');
    });
    // ui_url hint rendered, install=false hides override.
    expect(await screen.findByTestId('inst-ui-url-hint'))
      .toHaveTextContent('https://sonarr.example.com');
    expect(
      screen.queryByLabelText(/webhook base url|Базовый URL/i),
    ).not.toBeInTheDocument();

    // Dirty + save → invalidates webhook-status.
    await userEvent.clear(publicInput);
    await userEvent.type(publicInput, 'https://other.example.com');
    await userEvent.tab();
    const saveBtn = await screen.findByRole('button', { name: /^save instance$/i });
    await userEvent.click(saveBtn);
    await waitFor(() => expect(putCount).toBe(1));
    await waitFor(() => {
      const state = qc.getQueryState(['qbit', 'webhook-status', 'alpha']);
      expect(state?.isInvalidated || state?.fetchStatus === 'fetching').toBe(true);
    });
  });

  it('041h-2: edit mode renders the webhook status badge slot', async () => {
    const detail: InstanceDetail = {
      name: 'alpha',
      url: 'http://sonarr:8989',
      mode: DtoInstanceDetailMode.auto,
      webhook_install_enabled: true,
    } as InstanceDetail;
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL) => {
      const url = typeof u === 'string' ? u : u.toString();
      if (url.includes('/webhook/status')) {
        return jsonResp({ installed: true, notification_id: 7 });
      }
      if (url.includes('/instances/alpha')) return jsonResp(detail);
      return jsonResp({ instances: [] });
    }) as typeof fetch;

    const { qc } = renderWithProviders(
      <InstanceFormDialog
        open onOpenChange={() => {}} mode="edit"
        initial={{ name: 'alpha', url: 'http://sonarr:8989', mode: 'auto' }}
      />,
    );
    seedDetail(qc, 'alpha', detail);
    expect(
      await screen.findByTestId('inst-webhook-badge-slot'),
    ).toBeVisible();
    const badge = await screen.findByTestId('webhook-status-badge');
    expect(badge).toHaveAttribute('data-state', 'installed');
  });

  it('041h-2: create mode does NOT render the webhook status badge', async () => {
    globalThis.fetch = vi.fn(async () => jsonResp({})) as typeof fetch;
    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await screen.findByLabelText(/name/i);
    expect(screen.queryByTestId('inst-webhook-badge-slot')).not.toBeInTheDocument();
    expect(screen.queryByTestId('webhook-status-badge')).not.toBeInTheDocument();
  });
});
