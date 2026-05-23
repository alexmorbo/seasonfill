import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  firstScanRunId, NoScanStartedError, useTriggerScan, useCancelScan,
} from './scan-mutations';
import { DtoScanTriggerItemStatus } from '@/api/schema';

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

describe('firstScanRunId', () => {
  it('returns scan_run_id of the first element', () => {
    expect(
      firstScanRunId([
        {
          scan_run_id: 'abc-123',
          instance: 'alpha',
          status: DtoScanTriggerItemStatus.running,
        },
      ]),
    ).toBe('abc-123');
  });

  it('throws NoScanStartedError on empty array', () => {
    expect(() => firstScanRunId([])).toThrow(NoScanStartedError);
  });
});

describe('useTriggerScan()', () => {
  const origFetch = globalThis.fetch;
  beforeEach(() => {
    toastSuccess.mockClear();
    toastError.mockClear();
    toastMessage.mockClear();
    Object.defineProperty(window, 'location', {
      writable: true, value: { pathname: '/', assign: vi.fn() },
    });
  });
  afterEach(() => { globalThis.fetch = origFetch; });

  function wrap() {
    const qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false, gcTime: 0, staleTime: 0 },
        mutations: { retry: false },
      },
    });
    return ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
  }

  it('passes series_ids in the POST body when provided', async () => {
    const captured: { url?: string; body?: string } = {};
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof url === 'string' ? url : url.toString();
      if (typeof init?.body === 'string') captured.body = init.body;
      return new Response(
        JSON.stringify([{ scan_run_id: 'run-7', instance: 'alpha', status: 'running' }]),
        { status: 202, headers: { 'Content-Type': 'application/json' } },
      );
    }) as typeof fetch;

    const { result } = renderHook(() => useTriggerScan(), { wrapper: wrap() });
    result.current.mutate({ instance: 'alpha', series_ids: [122, 9] });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(captured.url).toBe('/api/v1/scan');
    expect(JSON.parse(captured.body ?? '{}')).toEqual({
      instance: 'alpha', series_ids: [122, 9],
    });
  });

  it('omits series_ids when caller does not provide it (back-compat)', async () => {
    const captured: { body?: string } = {};
    globalThis.fetch = vi.fn(async (_url, init?: RequestInit) => {
      if (typeof init?.body === 'string') captured.body = init.body;
      return new Response(
        JSON.stringify([{ scan_run_id: 'run-8', instance: 'alpha', status: 'running' }]),
        { status: 202, headers: { 'Content-Type': 'application/json' } },
      );
    }) as typeof fetch;

    const { result } = renderHook(() => useTriggerScan(), { wrapper: wrap() });
    result.current.mutate({ instance: 'alpha' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const parsed = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    expect(parsed).toEqual({ instance: 'alpha' });
    expect('series_ids' in parsed).toBe(false);
  });
});

describe('useCancelScan()', () => {
  const origFetch = globalThis.fetch;
  beforeEach(() => {
    toastSuccess.mockClear();
    toastError.mockClear();
    toastMessage.mockClear();
    Object.defineProperty(window, 'location', {
      writable: true, value: { pathname: '/', assign: vi.fn() },
    });
  });
  afterEach(() => { globalThis.fetch = origFetch; });

  // Local QC factory captures `invalidateQueries` calls (so the success
  // test can assert the right queries got nuked). Pattern lifted from
  // grab-mutation.test.tsx.
  function wrap() {
    const qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false, gcTime: 0, staleTime: 0 },
        mutations: { retry: false },
      },
    });
    const invalidations: unknown[][] = [];
    const orig = qc.invalidateQueries.bind(qc);
    qc.invalidateQueries = ((opts?: { queryKey?: readonly unknown[] }) => {
      if (opts?.queryKey) invalidations.push([...opts.queryKey]);
      return orig(opts);
    }) as typeof qc.invalidateQueries;
    const Wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
    return { invalidations, Wrapper };
  }

  const jsonResp = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

  it('POSTs /scans/:id/cancel and invalidates scans + scan keys on success', async () => {
    const captured: { url?: string; method?: string } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      if (init?.method) captured.method = init.method;
      return jsonResp({ ok: true }, 202);
    }) as typeof fetch;

    const { Wrapper, invalidations } = wrap();
    const { result } = renderHook(() => useCancelScan(), { wrapper: Wrapper });
    result.current.mutate({ id: 'scan-abc' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(captured.url).toBe('/api/v1/scans/scan-abc/cancel');
    expect(captured.method).toBe('POST');
    expect(toastSuccess).toHaveBeenCalledWith('Scan cancellation requested');
    expect(invalidations.flat()).toEqual(expect.arrayContaining(['scans', 'scan', 'scan-abc']));
  });

  it('404 surfaces as informational "already finished" toast (not error)', async () => {
    globalThis.fetch = vi.fn(async () => jsonResp({ error: 'scan not running' }, 404)) as typeof fetch;
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useCancelScan(), { wrapper: Wrapper });
    result.current.mutate({ id: 'scan-xyz' });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastMessage).toHaveBeenCalledWith('Scan already finished');
    expect(toastError).not.toHaveBeenCalled();
  });

  it('non-404 errors toast as Cancel failed: <message>', async () => {
    globalThis.fetch = vi.fn(async () => jsonResp({ error: 'database boom' }, 500)) as typeof fetch;
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useCancelScan(), { wrapper: Wrapper });
    result.current.mutate({ id: 'scan-xyz' });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastError).toHaveBeenCalledWith('Cancel failed: database boom');
  });
});
