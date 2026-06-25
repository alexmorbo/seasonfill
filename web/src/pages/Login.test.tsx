import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Login } from './Login';
import * as auth from '@/lib/auth';
import * as authConfig from '@/lib/auth-config';
import { ApiError } from '@/lib/api';

const navigateSpy = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigateSpy };
});

// Default-mock `useSession` to an unauthenticated state so we don't fire
// real network calls (would hit ECONNREFUSED on `localhost:3000` and stall
// `waitFor` past its default timeout). Story 539 B-48 tests override this
// to `isSuccess: true` to exercise the new authenticated-redirect path.
beforeEach(() => {
  vi.spyOn(auth, 'useSession').mockReturnValue({
    isPending: false,
    isError: true,
    isSuccess: false,
    data: undefined,
    error: new ApiError(401, 'unauthorized'),
  } as unknown as ReturnType<typeof auth.useSession>);
});

afterEach(() => {
  vi.restoreAllMocks();
  navigateSpy.mockReset();
});

type AuthCfgState = Partial<ReturnType<typeof authConfig.useAuthConfig>>;
function mockCfg(state: AuthCfgState) {
  vi.spyOn(authConfig, 'useAuthConfig').mockReturnValue({
    isPending: false,
    isSuccess: false,
    isError: false,
    data: undefined,
    error: null,
    ...state,
  } as ReturnType<typeof authConfig.useAuthConfig>);
}

async function fillAndSubmit(username = 'admin', password = 'hunter22') {
  await userEvent.type(screen.getByLabelText(/username/i), username);
  await userEvent.type(screen.getByLabelText(/password/i), password);
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
}

describe('<Login />', () => {
  // ────────────────────────────────────────────────────────────────────
  // PRESERVED — mode dispatch invariants from the legacy suite.
  // ────────────────────────────────────────────────────────────────────

  it('renders skeleton while auth-config is loading (no form flash)', () => {
    mockCfg({ isPending: true });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.queryByLabelText(/username/i)).toBeNull();
  });

  it('redirects to / when mode=basic', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'basic', localBypass: false, oidcReady: false } });
    renderWithProviders(<Login />, { route: '/login' });
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('renders entry button when mode=none (no redirect)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'none', localBypass: false, oidcReady: false } });
    renderWithProviders(<Login />, { route: '/login' });
    const btn = await screen.findByText(/enter the application/i);
    expect(btn).toBeInTheDocument();
    expect(navigateSpy).not.toHaveBeenCalled();
  });

  it('renders entry + SSO buttons when mode=none + oidcReady=true', async () => {
    mockCfg({
      isSuccess: true,
      data: {
        mode: 'none',
        localBypass: false,
        oidcReady: true,
        loginUrl: '/api/v1/auth/oidc/start',
      },
    });
    renderWithProviders(<Login />, { route: '/login' });
    expect(await screen.findByText(/enter the application/i)).toBeInTheDocument();
    expect(screen.getByTestId('oidc-login-link')).toBeInTheDocument();
  });

  it('renders form when mode=forms', () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByLabelText(/username/i)).toBeVisible();
  });

  it('renders form + SSO button when mode=forms + oidcReady=true', async () => {
    mockCfg({
      isSuccess: true,
      data: {
        mode: 'forms',
        localBypass: false,
        oidcReady: true,
        loginUrl: '/api/v1/auth/oidc/start',
      },
    });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByLabelText(/username/i)).toBeVisible();
    expect(await screen.findByTestId('oidc-login-link')).toBeInTheDocument();
  });

  it('falls back to form when /auth/config errors', () => {
    mockCfg({ isError: true, error: new ApiError(500, 'boom') });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByLabelText(/username/i)).toBeVisible();
  });

  // ────────────────────────────────────────────────────────────────────
  // PRESERVED — validation + submit + safeNext + error mapping.
  // ────────────────────────────────────────────────────────────────────

  it('shows validation errors when both fields are empty', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    const alerts = await screen.findAllByRole('alert');
    expect(alerts.length).toBeGreaterThanOrEqual(1);
    expect(alerts.map((a) => a.textContent).join(' ')).toMatch(/required/i);
  });

  it('navigates to / on success when no next param', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    const spy = vi.spyOn(auth, 'loginWithPassword').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit();
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith({ username: 'admin', password: 'hunter22' }),
    );
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('navigates to ?next= path on success', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'loginWithPassword').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login?next=%2Fscans%2Fabc' });
    await fillAndSubmit();
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/scans/abc', { replace: true }));
  });

  it('falls back to / when next is unsafe (//attacker)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'loginWithPassword').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login?next=%2F%2Fattacker.example' });
    await fillAndSubmit();
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('renders generic error on 401 (no enumeration)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'loginWithPassword').mockRejectedValue(new ApiError(401, 'unauthorized'));
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit('admin', 'wrong');
    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid credentials/i);
  });

  it('renders generic error on 429 (rate limit) — same wording as 401', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'loginWithPassword').mockRejectedValue(new ApiError(429, 'rate limit'));
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit('admin', 'wrong');
    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid credentials/i);
  });

  it('renders service-unavailable on 5xx', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'loginWithPassword').mockRejectedValue(new ApiError(503, 'down'));
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit();
    expect(await screen.findByRole('alert')).toHaveTextContent(/service unavailable/i);
  });

  it('renders SSO button when mode=oidc', async () => {
    mockCfg({
      isSuccess: true,
      data: {
        mode: 'oidc',
        localBypass: false,
        oidcReady: true,
        loginUrl: '/api/v1/auth/oidc/start',
      },
    });
    renderWithProviders(<Login />, { route: '/login' });
    const link = await screen.findByTestId('oidc-login-link');
    expect(link).toHaveAttribute('href', '/api/v1/auth/oidc/start');
  });

  it('appends ?next= to SSO href when present', async () => {
    mockCfg({
      isSuccess: true,
      data: {
        mode: 'oidc',
        localBypass: false,
        oidcReady: true,
        loginUrl: '/api/v1/auth/oidc/start',
      },
    });
    renderWithProviders(<Login />, { route: '/login?next=%2Finstances' });
    const link = await screen.findByTestId('oidc-login-link');
    expect(link).toHaveAttribute('href', '/api/v1/auth/oidc/start?next=%2Finstances');
  });

  it('does NOT redirect on mode=oidc (keeps user on login)', async () => {
    mockCfg({
      isSuccess: true,
      data: { mode: 'oidc', localBypass: false, oidcReady: true },
    });
    renderWithProviders(<Login />, { route: '/login' });
    await waitFor(() => expect(screen.queryByTestId('oidc-login-link')).toBeInTheDocument());
    expect(navigateSpy).not.toHaveBeenCalled();
  });

  // ────────────────────────────────────────────────────────────────────
  // NEW — Story 539 / B-48: redirect already-authenticated visitor.
  // ────────────────────────────────────────────────────────────────────

  it('redirects to / when session is already authenticated (B-48)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'useSession').mockReturnValue({
      isPending: false,
      isError: false,
      isSuccess: true,
      data: { username: 'admin', autoGenerated: false },
      error: null,
    } as unknown as ReturnType<typeof auth.useSession>);
    renderWithProviders(<Login />, { route: '/login' });
    await waitFor(() =>
      expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }),
    );
  });

  it('honors ?next= when redirecting an already-authenticated user (B-48)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'useSession').mockReturnValue({
      isPending: false,
      isError: false,
      isSuccess: true,
      data: { username: 'admin', autoGenerated: false },
      error: null,
    } as unknown as ReturnType<typeof auth.useSession>);
    renderWithProviders(<Login />, { route: '/login?next=%2Fscans' });
    await waitFor(() =>
      expect(navigateSpy).toHaveBeenCalledWith('/scans', { replace: true }),
    );
  });

  it('does NOT redirect when session is unauthenticated (B-48 negative)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    vi.spyOn(auth, 'useSession').mockReturnValue({
      isPending: false,
      isError: true,
      isSuccess: false,
      data: undefined,
      error: new Error('401'),
    } as unknown as ReturnType<typeof auth.useSession>);
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByLabelText(/username/i)).toBeVisible();
    expect(navigateSpy).not.toHaveBeenCalled();
  });

  // ────────────────────────────────────────────────────────────────────
  // NEW — F10 redesign chrome.
  // ────────────────────────────────────────────────────────────────────

  it('renders the redesigned card chrome — stage, glow, card, brand tile, foot', () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByTestId('login-stage')).toBeInTheDocument();
    expect(screen.getByTestId('login-glow')).toBeInTheDocument();
    expect(screen.getByTestId('login-card')).toBeInTheDocument();
    expect(screen.getByTestId('login-brand-tile')).toBeInTheDocument();
    expect(screen.getByTestId('login-foot')).toBeInTheDocument();
    // Brand row contains the wordmark (also appears in foot — assert ≥1).
    expect(screen.getAllByText(/seasonfill/i).length).toBeGreaterThanOrEqual(1);
  });

  it('foot omits mode label while config is loading; shows it once resolved', () => {
    mockCfg({ isPending: true });
    const { rerender } = renderWithProviders(<Login />, { route: '/login' });
    const foot = screen.getByTestId('login-foot');
    expect(foot.textContent).not.toMatch(/forms-auth|sso-only|no-auth|basic-auth/i);

    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    rerender(<Login />);
    expect(screen.getByTestId('login-foot').textContent).toMatch(/forms-auth/i);
  });

  it('forms pane uses icon-prefixed input pills with placeholders', () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false, oidcReady: false } });
    renderWithProviders(<Login />, { route: '/login' });
    const u = screen.getByLabelText(/username/i);
    const p = screen.getByLabelText(/password/i);
    expect(u).toHaveAttribute('autocomplete', 'username');
    expect(p).toHaveAttribute('autocomplete', 'current-password');
    expect(u).toHaveAttribute('placeholder');
    expect(p).toHaveAttribute('placeholder');
  });

  it('oidc-only pane renders intro copy and no password input', () => {
    mockCfg({
      isSuccess: true,
      data: {
        mode: 'oidc',
        localBypass: false,
        oidcReady: true,
        loginUrl: '/api/v1/auth/oidc/start',
      },
    });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.queryByLabelText(/password/i)).toBeNull();
    expect(screen.getAllByText(/single sign-on|sso/i).length).toBeGreaterThanOrEqual(1);
    expect(screen.getByTestId('oidc-login-link')).toBeInTheDocument();
  });
});
