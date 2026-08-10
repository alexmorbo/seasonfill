import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { useHideFromDiscovery } from './useHideFromDiscovery';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string, init?: unknown) => mockApi(p, init) };
});

const toastFn = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign((...args: unknown[]) => toastFn(...args), { error: (...a: unknown[]) => toastError(...a) }),
}));

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc },
      createElement(I18nextProvider, { i18n }, children));
}

beforeEach(() => {
  mockApi.mockReset();
  toastFn.mockReset();
  toastError.mockReset();
});

describe('useHideFromDiscovery', () => {
  it('POSTs the tmdb ref then raises an Undo toast whose action DELETEs + restores', async () => {
    mockApi.mockResolvedValueOnce({ id: 77, kind: 'tmdb', ref_id: 42 }); // POST
    mockApi.mockResolvedValueOnce(undefined); // DELETE on undo
    const onRestore = vi.fn();

    const { result } = renderHook(() => useHideFromDiscovery(), { wrapper: wrapper() });
    result.current.hide({ tmdbId: 42, title: 'Severance', onRestore });

    await waitFor(() => expect(toastFn).toHaveBeenCalledTimes(1));
    // POST fired with kind:tmdb + ref_id
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist',
      expect.objectContaining({ method: 'POST', body: { kind: 'tmdb', ref_id: 42 } }));

    // Pull the action off the toast options + invoke Undo.
    const opts = toastFn.mock.calls[0]?.[1] as { action: { onClick: () => void } };
    expect(typeof opts.action.onClick).toBe('function');
    opts.action.onClick();

    await waitFor(() => expect(mockApi).toHaveBeenCalledWith(
      '/discovery/blocklist/77', expect.objectContaining({ method: 'DELETE' })));
    expect(onRestore).toHaveBeenCalledTimes(1);
  });

  it('restores + error-toasts when the POST fails', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    const onRestore = vi.fn();
    const { result } = renderHook(() => useHideFromDiscovery(), { wrapper: wrapper() });
    result.current.hide({ tmdbId: 42, title: 'Severance', onRestore });
    await waitFor(() => expect(onRestore).toHaveBeenCalledTimes(1));
    expect(toastError).toHaveBeenCalledTimes(1);
    expect(toastFn).not.toHaveBeenCalled();
  });
});
