import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  useQbitSettings,
  useUpsertQbitSettings,
  useDiscoverQbit,
  qbitSettingsKey,
} from '../qbit';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

function makeQC() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
}

function wrap(qc: QueryClient) {
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const origFetch = globalThis.fetch;
beforeEach(() => {
  toastSuccess.mockClear();
  toastError.mockClear();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/settings', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('useQbitSettings', () => {
  it('returns null on 404 (no row yet)', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'not found', code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useQbitSettings('alpha'), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toBeNull();
  });

  it('returns the DTO on 200', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ instance_name: 'alpha', enabled: true, password_set: true, url: 'http://x' }, 200),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useQbitSettings('alpha'), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.password_set).toBe(true);
  });

  it('does not fire when name is null', async () => {
    const spy = vi.fn();
    globalThis.fetch = spy as typeof fetch;
    const qc = makeQC();
    renderHook(() => useQbitSettings(null), { wrapper: wrap(qc) });
    await new Promise((r) => setTimeout(r, 10));
    expect(spy).not.toHaveBeenCalled();
  });
});

describe('useUpsertQbitSettings', () => {
  it('PUTs the canonical body and toasts on success', async () => {
    const captured: { url?: string | undefined; method?: string | undefined; body?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ instance_name: 'alpha', url: 'http://q' }, 200);
    }) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useUpsertQbitSettings('alpha'), { wrapper: wrap(qc) });
    result.current.mutate({
      body: { url: 'http://q', category: 'sonarr', enabled: false } as never,
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.url).toBe('/api/v1/instances/alpha/qbit/settings');
    expect(captured.method).toBe('PUT');
    expect(JSON.parse(captured.body ?? '{}')).toMatchObject({ url: 'http://q' });
    expect(toastSuccess).toHaveBeenCalled();
  });

  it('maps 409 WEBHOOK_NOT_INSTALLED to the webhookRequired toast', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'webhook missing', code: 'WEBHOOK_NOT_INSTALLED' }, 409),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useUpsertQbitSettings('alpha'), { wrapper: wrap(qc) });
    result.current.mutate({ body: { enabled: true } as never });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastError).toHaveBeenCalledWith(
      'Install the webhook before enabling Watchdog.',
    );
  });

  it('invalidates the settings query on success', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ instance_name: 'alpha' }, 200),
    ) as typeof fetch;
    const qc = makeQC();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => useUpsertQbitSettings('alpha'), { wrapper: wrap(qc) });
    result.current.mutate({ body: { url: 'http://q' } as never });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(spy).toHaveBeenCalledWith({ queryKey: qbitSettingsKey('alpha') });
  });
});

describe('useDiscoverQbit', () => {
  it('does not fire when enabled is false', async () => {
    const spy = vi.fn();
    globalThis.fetch = spy as typeof fetch;
    const qc = makeQC();
    renderHook(() => useDiscoverQbit('alpha', { enabled: false }), { wrapper: wrap(qc) });
    await new Promise((r) => setTimeout(r, 10));
    expect(spy).not.toHaveBeenCalled();
  });

  it('resolves with the discover DTO when enabled flips to true', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ url: 'http://q', username: 'admin', category: 'sonarr', name: 'qbit' }, 200),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useDiscoverQbit('alpha', { enabled: true }), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.url).toBe('http://q');
  });
});
