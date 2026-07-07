import type { paths } from '@/api/schema';

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public body?: unknown,
    public headers?: Headers,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

type RequestInitWithJson = Omit<RequestInit, 'body'> & { body?: unknown };
const BASE = '/api/v1';

// authConfigCache stores the latest /auth/config response so the 401 handler
// can pick the correct login redirect without re-fetching on every 401.
// Seeded by lib/auth-config.ts via __seedAuthConfigCache; refreshed on a
// 401 when the cache is missing.
let authConfigCache: { mode: string; loginUrl?: string } | null = null;

export function __seedAuthConfigCache(v: { mode: string; loginUrl?: string }) {
  authConfigCache = v;
}

export function _resetAuthConfigCacheForTests() {
  authConfigCache = null;
}

async function refreshAuthConfig(): Promise<void> {
  try {
    const res = await fetch(`${BASE}/auth/config`, {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return;
    const j = (await res.json()) as { mode?: string; login_url?: string };
    authConfigCache = {
      mode: j.mode ?? 'forms',
      ...(j.login_url ? { loginUrl: j.login_url } : {}),
    };
  } catch {
    // fail-open
  }
}

function buildLoginRedirect(here: string): string | null {
  const cfg = authConfigCache;
  if (!cfg) return null;
  if (cfg.mode === 'basic' || cfg.mode === 'none') return null;
  const nextParam = here === '/' ? '' : '?next=' + encodeURIComponent(here);
  if (cfg.mode === 'oidc' && cfg.loginUrl) {
    return cfg.loginUrl + nextParam;
  }
  return '/login' + nextParam;
}

async function handle401(): Promise<void> {
  if (typeof window === 'undefined') return;
  if (window.location.pathname === '/login') return;
  if (window.location.pathname.startsWith('/api/v1/auth/oidc/')) return;
  const here = window.location.pathname + window.location.search;
  let target = buildLoginRedirect(here);
  if (target === null) {
    await refreshAuthConfig();
    target = buildLoginRedirect(here);
  }
  if (target !== null) {
    window.location.assign(target);
  }
}

export async function api<T>(path: string, init: RequestInitWithJson = {}): Promise<T> {
  const { body, headers, ...rest } = init;
  // NOTE: no `cache` option is set, so GETs use the browser's default cache mode.
  // This is REQUIRED for the /series/:id weak-ETag 304 path (W18-16): the browser
  // stores the ETag and revalidates with If-None-Match transparently, returning
  // the cached body on a 304 (JS never sees the 304). Do NOT add
  // `cache: 'no-store'` to GETs — it disables conditional requests and re-churns
  // the (now stable) skeleton body on every view.
  const res = await fetch(`${BASE}${path}`, {
    ...rest,
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...(headers ?? {}),
    },
    ...(body !== undefined && { body: JSON.stringify(body) }),
  });

  if (res.status === 401) {
    await handle401();
    throw new ApiError(401, 'unauthorized');
  }
  if (!res.ok) {
    let parsed: unknown;
    try { parsed = await res.json(); } catch { parsed = await res.text().catch(() => undefined); }
    const msg = typeof parsed === 'object' && parsed && 'error' in parsed
      ? String((parsed as { error: unknown }).error)
      : res.statusText;
    throw new ApiError(res.status, msg, parsed);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export type ApiPath = keyof paths;
