import { describe, expect, it, vi, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Login } from './Login';
import * as auth from '@/lib/auth';
import { ApiError } from '@/lib/api';

afterEach(() => { vi.restoreAllMocks(); });

describe('<Login />', () => {
  it('shows zod error when api_key is empty', async () => {
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/api key required/i);
  });

  it('calls login() and navigates on success', async () => {
    const spy = vi.spyOn(auth, 'login').mockResolvedValue(undefined);
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.type(screen.getByLabelText(/api key/i), 'sf_test');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith('sf_test'));
  });

  it('renders server error on 401', async () => {
    vi.spyOn(auth, 'login').mockRejectedValue(new ApiError(401, 'unauthorized'));
    renderWithProviders(<Login />, { route: '/login' });
    await userEvent.type(screen.getByLabelText(/api key/i), 'bogus');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid api key/i);
  });
});
