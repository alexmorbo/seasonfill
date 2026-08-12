import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import * as meModule from '@/hooks/useMe';
import { Requests } from './Requests';
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

const tvRow = {
  id: 7, user_id: 2, username: 'alice', media_type: 'tv', tmdb_id: 1399,
  title: 'Breaking Bad', seasons: [1, 2], status: 'pending',
  created_at: '2026-08-12T12:00:00Z',
};
const movieRow = {
  id: 8, user_id: 3, username: '', media_type: 'movie', tmdb_id: 603,
  status: 'approved', approver_id: 1, created_at: '2026-08-11T12:00:00Z',
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const origFetch = globalThis.fetch;

function installFetch(captured: { calls: { url: string; method: string }[] }) {
  globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof u === 'string' ? u : u.toString();
    const method = init?.method ?? 'GET';
    captured.calls.push({ url, method });
    if (url.includes('/requests/7/approve')) return json({ ...tvRow, status: 'approved' });
    if (url.includes('/requests/7/deny')) return json({ ...tvRow, status: 'denied' });
    if (url.endsWith('/requests')) return json({ items: [tvRow, movieRow] });
    return json({});
  }) as typeof fetch;
}

beforeEach(() => {
  toastSuccess.mockClear();
  toastError.mockClear();
  vi.restoreAllMocks();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/requests', search: '', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<Requests />', () => {
  it('renders the pending row (with actions) and the resolved row', async () => {
    mockMe();
    installFetch({ calls: [] });
    renderWithProviders(<Requests />, { route: '/requests' });

    expect(await screen.findByText('Breaking Bad')).toBeInTheDocument();
    // pending tv row exposes approve + deny buttons
    expect(screen.getByTestId('request-approve-7')).toBeInTheDocument();
    expect(screen.getByTestId('request-deny-7')).toBeInTheDocument();
    // approved movie row: no action buttons
    expect(screen.queryByTestId('request-approve-8')).not.toBeInTheDocument();
    // seasons render for the tv row; username empty falls back to #user_id
    expect(screen.getByTestId('request-seasons-7')).toHaveTextContent('1, 2');
    expect(screen.getByText('#3')).toBeInTheDocument();
    // movie with no title falls back to #tmdb_id
    expect(screen.getByText('#603')).toBeInTheDocument();
  });

  it('Approve shows a confirm dialog BEFORE calling approve(id)', async () => {
    mockMe();
    const captured = { calls: [] as { url: string; method: string }[] };
    installFetch(captured);
    renderWithProviders(<Requests />, { route: '/requests' });

    await userEvent.click(await screen.findByTestId('request-approve-7'));
    // dialog visible, but no approve POST yet
    expect(await screen.findByTestId('request-approve-dialog')).toBeInTheDocument();
    expect(captured.calls.some((c) => c.url.includes('/requests/7/approve'))).toBe(false);

    await userEvent.click(await screen.findByTestId('request-approve-confirm-7'));
    await waitFor(() =>
      expect(captured.calls.some((c) => c.url.includes('/requests/7/approve') && c.method === 'POST')).toBe(true),
    );
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
  });

  it('Deny confirm POSTs to deny(id)', async () => {
    mockMe();
    const captured = { calls: [] as { url: string; method: string }[] };
    installFetch(captured);
    renderWithProviders(<Requests />, { route: '/requests' });

    await userEvent.click(await screen.findByTestId('request-deny-7'));
    expect(await screen.findByTestId('request-deny-dialog')).toBeInTheDocument();
    await userEvent.click(await screen.findByTestId('request-deny-confirm-7'));
    await waitFor(() =>
      expect(captured.calls.some((c) => c.url.includes('/requests/7/deny') && c.method === 'POST')).toBe(true),
    );
  });

  it('blocks non-admins with a denied panel and no table', async () => {
    mockMe({ role: 'user' });
    installFetch({ calls: [] });
    renderWithProviders(<Requests />, { route: '/requests' });

    expect(await screen.findByTestId('requests-access-denied')).toBeInTheDocument();
    expect(screen.queryByTestId('requests-table')).not.toBeInTheDocument();
  });

  it('shows a loading placeholder while /me is in flight', () => {
    mockMe({}, { isLoading: true });
    installFetch({ calls: [] });
    renderWithProviders(<Requests />, { route: '/requests' });
    expect(screen.queryByTestId('requests-table')).not.toBeInTheDocument();
    expect(screen.queryByTestId('requests-access-denied')).not.toBeInTheDocument();
  });
});
