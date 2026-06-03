import { describe, expect, it, vi, afterEach } from 'vitest';
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

afterEach(() => { vi.restoreAllMocks(); navigateSpy.mockReset(); });

type AuthCfgState = Partial<ReturnType<typeof authConfig.useAuthConfig>>;
function mockCfg(state: AuthCfgState) {
  vi.spyOn(authConfig, 'useAuthConfig').mockReturnValue({
    isPending: false, isSuccess: false, isError: false,
    data: undefined, error: null,
    ...state,
  } as ReturnType<typeof authConfig.useAuthConfig>);
}

async function fillAndSubmit(username = 'admin', password = 'hunter22') {
  await userEvent.type(screen.getByLabelText(/username/i), username);
  await userEvent.type(screen.getByLabelText(/password/i), password);
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
}

describe('<Login />', () => {
  it('renders skeleton while auth-config is loading (no form flash)', () => {
    mockCfg({ isPending: true });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.queryByLabelText(/username/i)).toBeNull();
  });

  it('redirects to / when mode=basic', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'basic', localBypass: false } });
    renderWithProviders(<Login />, { route: '/login' });
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('redirects to / when mode=none', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'none', localBypass: false } });
    renderWithProviders(<Login />, { route: '/login' });
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('renders form when mode=forms', () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByLabelText(/username/i)).toBeVisible();
  });

  it('falls back to form when /auth/config errors', () => {
    mockCfg({ isError: true, error: new ApiError(500, 'boom') });
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByLabelText(/username/i)).toBeVisible();
  });

  it('shows validation errors when both fields are empty', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    const alerts = await screen.findAllByRole('alert');
    expect(alerts.length).toBeGreaterThanOrEqual(1);
    expect(alerts.map((a) => a.textContent).join(' ')).toMatch(/required/i);
  });

  it('navigates to / on success when no next param', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    const spy = vi.spyOn(auth, 'loginWithPassword').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit();
    await waitFor(() => expect(spy).toHaveBeenCalledWith({ username: 'admin', password: 'hunter22' }));
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('navigates to ?next= path on success', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    vi.spyOn(auth, 'loginWithPassword').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login?next=%2Fscans%2Fabc' });
    await fillAndSubmit();
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/scans/abc', { replace: true }));
  });

  it('falls back to / when next is unsafe (//attacker)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    vi.spyOn(auth, 'loginWithPassword').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login?next=%2F%2Fattacker.example' });
    await fillAndSubmit();
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('renders generic error on 401 (no enumeration)', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    vi.spyOn(auth, 'loginWithPassword').mockRejectedValue(new ApiError(401, 'unauthorized'));
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit('admin', 'wrong');
    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid credentials/i);
  });

  it('renders generic error on 429 (rate limit) — same wording as 401', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    vi.spyOn(auth, 'loginWithPassword').mockRejectedValue(new ApiError(429, 'rate limit'));
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit('admin', 'wrong');
    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid credentials/i);
  });

  it('renders service-unavailable on 5xx', async () => {
    mockCfg({ isSuccess: true, data: { mode: 'forms', localBypass: false } });
    vi.spyOn(auth, 'loginWithPassword').mockRejectedValue(new ApiError(503, 'down'));
    renderWithProviders(<Login />, { route: '/login' });
    await fillAndSubmit();
    expect(await screen.findByRole('alert')).toHaveTextContent(/service unavailable/i);
  });
});
