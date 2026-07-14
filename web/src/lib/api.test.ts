import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from './api';

describe('api()', () => {
  const origFetch = globalThis.fetch;
  const origLocation = window.location;

  beforeEach(() => {
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

  it('redirects to /login on 401, preserving the current path as next', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).toHaveBeenCalledWith('/login?next=' + encodeURIComponent('/dashboard'));
  });

  it('redirects to /login without a next param when on root', async () => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { ...origLocation, pathname: '/', search: '', assign: vi.fn() },
    });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).toHaveBeenCalledWith('/login');
  });

  it('does NOT redirect when already on /login', async () => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { ...origLocation, pathname: '/login', assign: vi.fn() },
    });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/auth/login', { method: 'POST', body: { api_key: 'x' } })).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).not.toHaveBeenCalled();
  });

  it('does NOT redirect while on an OIDC callback path', async () => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { ...origLocation, pathname: '/api/v1/auth/oidc/callback', search: '', assign: vi.fn() },
    });
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
    await expect(api('/scans')).rejects.toBeInstanceOf(ApiError);
    expect(window.location.assign).not.toHaveBeenCalled();
  });

  it('wraps non-2xx error body into ApiError', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(new Response('{"error":"bad request"}', {
      status: 400, headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch;
    await expect(api('/scan')).rejects.toMatchObject({ status: 400, message: 'bad request' });
  });
});

describe('api() HTTP-cache participation (W18-16)', () => {
  const origFetch = globalThis.fetch;
  afterEach(() => {
    globalThis.fetch = origFetch;
  });

  it('does not force cache:no-store on GET (keeps ETag/304 alive)', async () => {
    const spy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    globalThis.fetch = spy as typeof fetch;
    await api('/series/42?lang=ru-RU');
    expect(spy).toHaveBeenCalledTimes(1);
    const init = spy.mock.calls[0]?.[1] as RequestInit | undefined;
    // default (undefined) or an explicit non-'no-store' mode both preserve the
    // browser conditional-request path; only 'no-store' breaks it.
    expect(init?.cache).not.toBe('no-store');
  });
});
