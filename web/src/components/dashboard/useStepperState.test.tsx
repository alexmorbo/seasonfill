import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { useStepperState } from './useStepperState';
import * as externalServicesApi from '@/api/externalServices';

const origFetch = globalThis.fetch;

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function wrap({ children }: { children: ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe('useStepperState', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/admin/instances')) {
        return jsonResponse({ instances: [] });
      }
      if (url.includes('/webhooks/status')) {
        return jsonResponse({ items: [], healthy_count: 0, unhealthy_count: 0 });
      }
      if (url.includes('/scans')) {
        return jsonResponse({ items: [] });
      }
      return jsonResponse({});
    }) as never;
    vi.spyOn(externalServicesApi, 'listExternalServices').mockResolvedValue([]);
  });

  afterEach(() => {
    globalThis.fetch = origFetch;
  });

  it('all-todo when zero instances and no services and no scans', async () => {
    const { result } = renderHook(() => useStepperState(), { wrapper: wrap });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    const byId = new Map(result.current.steps.map((s) => [s.id, s.status]));
    expect(byId.get('sonarr')).toBe('todo');
    expect(byId.get('webhook')).toBe('todo');
    expect(byId.get('tmdb')).toBe('todo');
    expect(byId.get('omdb')).toBe('todo');
    expect(byId.get('scan')).toBe('todo');
    expect(result.current.allRequiredDone).toBe(false);
  });

  it('webhook in_progress when instance is Bootstrapping', async () => {
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/admin/instances')) {
        return jsonResponse({
          instances: [{ name: 'a', health: 'Bootstrapping' }],
        });
      }
      if (url.includes('/webhooks/status')) {
        return jsonResponse({ items: [], healthy_count: 0, unhealthy_count: 0 });
      }
      if (url.includes('/scans')) {
        return jsonResponse({ items: [] });
      }
      return jsonResponse({});
    }) as never;
    const { result } = renderHook(() => useStepperState(), { wrapper: wrap });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    const byId = new Map(result.current.steps.map((s) => [s.id, s.status]));
    expect(byId.get('sonarr')).toBe('done');
    expect(byId.get('webhook')).toBe('in_progress');
  });

  it('webhook done when /webhooks/status reports any healthy+installed', async () => {
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/admin/instances')) {
        return jsonResponse({
          instances: [{ name: 'a', health: 'Available' }],
        });
      }
      if (url.includes('/webhooks/status')) {
        return jsonResponse({
          items: [{ instance_name: 'a', installed: true, healthy: true }],
          healthy_count: 1, unhealthy_count: 0,
        });
      }
      if (url.includes('/scans')) {
        return jsonResponse({ items: [] });
      }
      return jsonResponse({});
    }) as never;
    const { result } = renderHook(() => useStepperState(), { wrapper: wrap });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.steps.find((s) => s.id === 'webhook')?.status).toBe('done');
  });

  it('tmdb error when invalid_key', async () => {
    vi.spyOn(externalServicesApi, 'listExternalServices').mockResolvedValue([
      {
        service: 'tmdb', enabled: true, api_key_configured: true,
        api_key_masked: 'abcd…', proxy_url_set: false, proxy_auth_set: false,
        last_validation_status: 'invalid_key',
      },
    ]);
    const { result } = renderHook(() => useStepperState(), { wrapper: wrap });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.steps.find((s) => s.id === 'tmdb')?.status).toBe('error');
  });

  it('tmdb done when valid', async () => {
    vi.spyOn(externalServicesApi, 'listExternalServices').mockResolvedValue([
      {
        service: 'tmdb', enabled: true, api_key_configured: true,
        api_key_masked: 'abcd…', proxy_url_set: false, proxy_auth_set: false,
        last_validation_status: 'valid',
      },
    ]);
    const { result } = renderHook(() => useStepperState(), { wrapper: wrap });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.steps.find((s) => s.id === 'tmdb')?.status).toBe('done');
  });

  it('scan in_progress when running', async () => {
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/admin/instances')) {
        return jsonResponse({
          instances: [{ name: 'a', health: 'Available' }],
        });
      }
      if (url.includes('/webhooks/status')) {
        return jsonResponse({
          items: [{ instance_name: 'a', installed: true, healthy: true }],
          healthy_count: 1, unhealthy_count: 0,
        });
      }
      if (url.includes('/scans')) {
        return jsonResponse({ items: [{ id: 'r1', status: 'running' }] });
      }
      return jsonResponse({});
    }) as never;
    const { result } = renderHook(() => useStepperState(), { wrapper: wrap });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.steps.find((s) => s.id === 'scan')?.status).toBe('in_progress');
  });

  it('allRequiredDone=true when sonarr+webhook+tmdb+scan done (omdb skipped)', async () => {
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/admin/instances')) {
        return jsonResponse({
          instances: [{ name: 'a', health: 'Available' }],
        });
      }
      if (url.includes('/webhooks/status')) {
        return jsonResponse({
          items: [{ instance_name: 'a', installed: true, healthy: true }],
          healthy_count: 1, unhealthy_count: 0,
        });
      }
      if (url.includes('/scans')) {
        return jsonResponse({ items: [{ id: 'r1', status: 'completed' }] });
      }
      return jsonResponse({});
    }) as never;
    vi.spyOn(externalServicesApi, 'listExternalServices').mockResolvedValue([
      {
        service: 'tmdb', enabled: true, api_key_configured: true,
        api_key_masked: 'abcd…', proxy_url_set: false, proxy_auth_set: false,
        last_validation_status: 'valid',
      },
    ]);
    const { result } = renderHook(() => useStepperState(), { wrapper: wrap });
    await waitFor(() => expect(result.current.allRequiredDone).toBe(true));
  });
});
