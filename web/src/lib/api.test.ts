import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, _resetAuthConfigCacheForTests, __seedAuthConfigCache } from './api';

describe('api()', () => {
  const origFetch = globalThis.fetch;
  const origLocation = window.location;

  beforeEach(() => {
    _resetAuthConfigCacheForTests();
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { ...origLocation, pathname: '/dashboard', search: '', assign: vi.fn() },
    });
  });
  afterEach(() => {
    globalThis.fetch = origFetch;
    Object.defineProperty(window, 'location', { writable: true, value: origLocation });
  });

  it('returns parsed JSON on 2xx', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch;
    await expect(api<{ ok: boolean }>('/auth/session')).resolves.toEqual({ ok: true });
  });

  it('returns undefined on 204', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 })) as typeof fetch;
    await expect(api<void>('/auth/session', { method: 'DELETE' })).resolves.toBeUndefined();
  });

  it('redirects to OIDC start when mode=oidc and cache is seeded', async () => {
    __seedAuthConfigCache({ mode: 'oidc', loginUrl: '/api/v1/auth/oidc/start' });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).toHaveBeenCalledWith('/api/v1/auth/oidc/start?next=' + encodeURIComponent('/dashboard'));
  });

  it('redirects to /login when mode=forms and cache is seeded', async () => {
    __seedAuthConfigCache({ mode: 'forms' });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).toHaveBeenCalledWith('/login?next=' + encodeURIComponent('/dashboard'));
  });

  it('does NOT redirect when mode=basic', async () => {
    __seedAuthConfigCache({ mode: 'basic' });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).not.toHaveBeenCalled();
  });

  it('does NOT redirect when mode=none', async () => {
    __seedAuthConfigCache({ mode: 'none' });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).not.toHaveBeenCalled();
  });

  it('does NOT redirect when already on /login', async () => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { ...origLocation, pathname: '/login', assign: vi.fn() },
    });
    __seedAuthConfigCache({ mode: 'forms' });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/auth/login', { method: 'POST', body: { api_key: 'x' } })).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).not.toHaveBeenCalled();
  });

  it('fetches /auth/config on 401 when cache is missing, then redirects (forms)', async () => {
    // No __seedAuthConfigCache call — cache is null
    let callCount = 0;
    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      callCount++;
      if (typeof url === 'string' && url.includes('/auth/config')) {
        return Promise.resolve(new Response(JSON.stringify({ mode: 'forms' }), {
          status: 200, headers: { 'Content-Type': 'application/json' },
        }));
      }
      return Promise.resolve(new Response('{"error":"unauthorized"}', { status: 401 }));
    }) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(callCount).toBeGreaterThanOrEqual(2); // original + /auth/config
    expect(window.location.assign).toHaveBeenCalledWith('/login?next=' + encodeURIComponent('/dashboard'));
  });

  it('wraps non-2xx error body into ApiError', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"bad request"}', {
      status: 400, headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch;
    await expect(api('/scan')).rejects.toMatchObject({ status: 400, message: 'bad request' });
  });
});
