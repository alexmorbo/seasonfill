import { describe, expect, it, vi, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Login } from './Login';
import * as auth from '@/lib/auth';
import { ApiError } from '@/lib/api';

const navigateSpy = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigateSpy };
});

afterEach(() => { vi.restoreAllMocks(); navigateSpy.mockReset(); });

describe('<Login />', () => {
  it('shows zod error when api_key is empty', async () => {
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/api key required/i);
  });

  it('input has autoComplete=off', () => {
    renderWithProviders(<Login />, { route: '/login' });
    expect(screen.getByLabelText(/api key/i)).toHaveAttribute('autocomplete', 'off');
  });

  it('navigates to / on success when no next param', async () => {
    vi.spyOn(auth, 'login').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.type(screen.getByLabelText(/api key/i), 'sf_test');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('navigates to ?next= path on success', async () => {
    vi.spyOn(auth, 'login').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login?next=%2Fscans%2Fabc' });
    await userEvent.type(screen.getByLabelText(/api key/i), 'sf_test');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/scans/abc', { replace: true }));
  });

  it('falls back to / when next is unsafe (//attacker)', async () => {
    vi.spyOn(auth, 'login').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login?next=%2F%2Fattacker.example' });
    await userEvent.type(screen.getByLabelText(/api key/i), 'sf_test');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(navigateSpy).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('renders server error on 401', async () => {
    vi.spyOn(auth, 'login').mockRejectedValue(new ApiError(401, 'unauthorized'));
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.type(screen.getByLabelText(/api key/i), 'bogus');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid api key/i);
  });
});
