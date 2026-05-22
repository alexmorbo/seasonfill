import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from './api';

describe('api()', () => {
  const origFetch = globalThis.fetch;
  const origLocation = window.location;

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { ...origLocation, pathname: '/', search: '', assign: vi.fn() },
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

  it('redirects to /login (no next) when at /', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/instances')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).toHaveBeenCalledWith('/login');
  });

  it('redirects to /login?next=<encoded> when at a deeper route', async () => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { ...origLocation, pathname: '/scans/abc', search: '?foo=1', assign: vi.fn() },
    });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).toHaveBeenCalledWith('/login?next=' + encodeURIComponent('/scans/abc?foo=1'));
  });

  it('does NOT redirect when already on /login', async () => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { ...origLocation, pathname: '/login', assign: vi.fn() },
    });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/auth/login', { method: 'POST', body: { api_key: 'x' } })).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).not.toHaveBeenCalled();
  });

  it('wraps non-2xx error body into ApiError', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"bad request"}', {
      status: 400, headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch;
    await expect(api('/scan')).rejects.toMatchObject({ status: 400, message: 'bad request' });
  });
});
