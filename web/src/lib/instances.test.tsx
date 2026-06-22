import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useInstances } from './instances';

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useInstances()', () => {
  const origFetch = globalThis.fetch;
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { pathname: '/', assign: vi.fn() },
    });
  });
  afterEach(() => { globalThis.fetch = origFetch; });

  it('returns parsed InstanceList on 200', async () => {
    const payload = { instances: [{ name: 'alpha', health: 'available' }] };
    globalThis.fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch;
    const { result } = renderHook(() => useInstances(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.instances?.[0]?.name).toBe('alpha');
  });

  // Story 488 (B-14): when any instance is Bootstrapping, the list
  // refetches every 2s instead of every 30s so the operator sees the
  // pill flip to Available within ~2s of the first preflight passing.
  it('refetches ~every 2s while any instance is Bootstrapping; ~every 30s otherwise', async () => {
    const fetchSpy = vi.fn().mockImplementation((): Promise<Response> => {
      const payload = { instances: [{ name: 'alpha', health: 'Bootstrapping' }] };
      return Promise.resolve(new Response(JSON.stringify(payload), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }));
    });
    globalThis.fetch = fetchSpy as typeof fetch;
    const { result } = renderHook(() => useInstances(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const callsAfterMount = fetchSpy.mock.calls.length;
    // The 2s fast-poll should land at least one more fetch within ~3s.
    await waitFor(() => {
      expect(fetchSpy.mock.calls.length).toBeGreaterThan(callsAfterMount);
    }, { timeout: 4000 });
  }, 10_000);

  it('falls back to the 30s steady poll when no instance is Bootstrapping', async () => {
    const fetchSpy = vi.fn().mockImplementation((): Promise<Response> => {
      const payload = { instances: [{ name: 'alpha', health: 'Available' }] };
      return Promise.resolve(new Response(JSON.stringify(payload), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }));
    });
    globalThis.fetch = fetchSpy as typeof fetch;
    const { result } = renderHook(() => useInstances(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const callsAfterMount = fetchSpy.mock.calls.length;
    // 3s window — fast-poll would have fired by now if active.
    await new Promise((r) => setTimeout(r, 3000));
    expect(fetchSpy.mock.calls.length).toBe(callsAfterMount);
  }, 10_000);
});
