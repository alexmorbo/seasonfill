import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import * as meModule from '@/hooks/useMe';
import { Users } from './Users';
import type { MeResponse } from '@/lib/me-types';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

const baseMe = (overrides: Partial<MeResponse> = {}): MeResponse => ({
  id: 1,
  username: 'admin',
  email: 'admin@example.com',
  role: 'admin',
  auth_mode: 'forms',
  avatar_mode: 'auto',
  avatar_resolved_mode: 'gravatar',
  avatar_hash: '0bc83cb571cd1c50ba6f3e8a78ef1346',
  preferred_language: null,
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: null,
  permissions: {
    auto_approve: true, request: true, manage_requests: true,
    manage_users: true, request_4k: true,
  },
  ...overrides,
});

function mockMe(overrides: Partial<MeResponse> = {}, extra: { isLoading?: boolean } = {}) {
  vi.spyOn(meModule, 'useMe').mockReturnValue({
    data: extra.isLoading ? undefined : baseMe(overrides),
    isLoading: extra.isLoading ?? false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof meModule.useMe>);
}

const adminRow = {
  id: 1, username: 'admin', email: 'admin@example.com', role: 'admin',
  auth_source: 'forms',
  permissions: {
    auto_approve: true, request: true, manage_requests: true,
    manage_users: true, request_4k: true,
  },
  last_login_at: '2026-08-12T12:00:00Z', created_at: '2026-01-01T00:00:00Z',
};
const aliceRow = {
  id: 3, username: 'alice', email: 'alice@example.com', role: 'user',
  auth_source: 'jellyfin',
  permissions: {
    auto_approve: false, request: false, manage_requests: false,
    manage_users: false, request_4k: false,
  },
  last_login_at: null, created_at: '2026-02-01T00:00:00Z',
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const origFetch = globalThis.fetch;

function installFetch(
  captured: { calls: { url: string; method: string }[] },
  opts: { patchStatus?: number; patchBody?: unknown } = {},
) {
  globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof u === 'string' ? u : u.toString();
    const method = init?.method ?? 'GET';
    captured.calls.push({ url, method });
    if (url.includes('/admin/users/3') && method === 'PATCH') {
      const status = opts.patchStatus ?? 200;
      const body = opts.patchBody ?? { ...aliceRow };
      return json(body, status);
    }
    if (url.includes('/admin/users/3') && method === 'DELETE') return json(null, 204);
    if (url.endsWith('/admin/users')) return json({ items: [adminRow, aliceRow] });
    return json({});
  }) as typeof fetch;
}

beforeEach(() => {
  toastSuccess.mockClear();
  toastError.mockClear();
  vi.restoreAllMocks();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/users', search: '', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<Users />', () => {
  it('renders the user rows with permission switches', async () => {
    mockMe();
    installFetch({ calls: [] });
    renderWithProviders(<Users />, { route: '/users' });

    expect(await screen.findByText('alice')).toBeInTheDocument();
    expect(screen.getByTestId('user-row-1')).toBeInTheDocument();
    expect(screen.getByTestId('user-perm-request-3')).toBeInTheDocument();
    expect(screen.getByTestId('user-perm-request-3')).toHaveAttribute('aria-checked', 'false');
  });

  it('toggling a permission Switch PATCHes /admin/users/:id', async () => {
    mockMe();
    const captured = { calls: [] as { url: string; method: string }[] };
    installFetch(captured);
    renderWithProviders(<Users />, { route: '/users' });

    await userEvent.click(await screen.findByTestId('user-perm-request-3'));
    await waitFor(() =>
      expect(captured.calls.some((c) =>
        c.url.includes('/admin/users/3') && c.method === 'PATCH')).toBe(true),
    );
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
  });

  it('delete confirm DELETEs /admin/users/:id', async () => {
    mockMe();
    const captured = { calls: [] as { url: string; method: string }[] };
    installFetch(captured);
    renderWithProviders(<Users />, { route: '/users' });

    await userEvent.click(await screen.findByTestId('user-delete-3'));
    expect(await screen.findByTestId('user-delete-dialog')).toBeInTheDocument();
    await userEvent.click(await screen.findByTestId('user-delete-confirm-3'));
    await waitFor(() =>
      expect(captured.calls.some((c) =>
        c.url.includes('/admin/users/3') && c.method === 'DELETE')).toBe(true),
    );
  });

  it('reverts the Switch and toasts on a 409', async () => {
    mockMe();
    const captured = { calls: [] as { url: string; method: string }[] };
    installFetch(captured, {
      patchStatus: 409,
      patchBody: { error: 'self lockout', code: 'SELF_LOCKOUT' },
    });
    renderWithProviders(<Users />, { route: '/users' });

    const sw = await screen.findByTestId('user-perm-request-3');
    expect(sw).toHaveAttribute('aria-checked', 'false');
    await userEvent.click(sw);

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByTestId('user-perm-request-3')).toHaveAttribute('aria-checked', 'false'),
    );
  });

  it('blocks users without manage_users with a denied panel', async () => {
    mockMe({
      role: 'user',
      permissions: {
        auto_approve: false, request: true, manage_requests: false,
        manage_users: false, request_4k: false,
      },
    });
    installFetch({ calls: [] });
    renderWithProviders(<Users />, { route: '/users' });

    expect(await screen.findByTestId('users-access-denied')).toBeInTheDocument();
    expect(screen.queryByTestId('users-table')).not.toBeInTheDocument();
  });
});
