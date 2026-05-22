import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { firstScanRunId, NoScanStartedError, useTriggerScan } from './scan-mutations';
import { DtoScanTriggerItemStatus } from '@/api/schema';

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
