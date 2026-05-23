import { describe, expect, it, vi, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { PasswordChangeDialog } from './PasswordChangeDialog';
import * as auth from '@/lib/auth';
import { ApiError } from '@/lib/api';

const { toastSpies } = vi.hoisted(() => ({
  toastSpies: { success: vi.fn(), error: vi.fn() },
}));
vi.mock('sonner', () => ({ toast: toastSpies }));

afterEach(() => { vi.restoreAllMocks(); toastSpies.success.mockReset(); toastSpies.error.mockReset(); });

async function fill(current: string, newp: string, confirm: string) {
  await userEvent.type(screen.getByLabelText(/current password/i), current);
  await userEvent.type(screen.getByLabelText(/^new password$/i), newp);
  await userEvent.type(screen.getByLabelText(/confirm new password/i), confirm);
}

describe('<PasswordChangeDialog />', () => {
  it('rejects new password shorter than 8 chars', async () => {
    renderWithProviders(<PasswordChangeDialog open onOpenChange={() => {}} />);
    await fill('oldpass', 'short', 'short');
    await userEvent.click(screen.getByRole('button', { name: /update password/i }));
    expect(await screen.findByText(/min 8 characters/i)).toBeInTheDocument();
  });

  it('rejects when confirm does not match', async () => {
    renderWithProviders(<PasswordChangeDialog open onOpenChange={() => {}} />);
    await fill('oldpass', 'longenoughpw', 'longenoughpx');
    await userEvent.click(screen.getByRole('button', { name: /update password/i }));
    expect(await screen.findByText(/do not match/i)).toBeInTheDocument();
  });

  it('on 204 closes dialog + shows toast', async () => {
    const onOpenChange = vi.fn();
    const spy = vi.spyOn(auth, 'changePassword').mockResolvedValue(undefined);
    renderWithProviders(<PasswordChangeDialog open onOpenChange={onOpenChange} />);
    await fill('oldpass', 'longenoughpw', 'longenoughpw');
    await userEvent.click(screen.getByRole('button', { name: /update password/i }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith({ current: 'oldpass', newPassword: 'longenoughpw' }));
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(toastSpies.success).toHaveBeenCalledWith('Password updated');
  });

  it('on 401 shows "Current password is incorrect"', async () => {
    vi.spyOn(auth, 'changePassword').mockRejectedValue(new ApiError(401, 'Invalid credentials'));
    renderWithProviders(<PasswordChangeDialog open onOpenChange={() => {}} />);
    await fill('wrong', 'longenoughpw', 'longenoughpw');
    await userEvent.click(screen.getByRole('button', { name: /update password/i }));
    expect(await screen.findByText(/current password is incorrect/i)).toBeInTheDocument();
  });

  it('on 400 surfaces server error message', async () => {
    vi.spyOn(auth, 'changePassword').mockRejectedValue(new ApiError(400, 'password too short (min 8 chars)'));
    renderWithProviders(<PasswordChangeDialog open onOpenChange={() => {}} />);
    await fill('oldpass', 'longenoughpw', 'longenoughpw');
    await userEvent.click(screen.getByRole('button', { name: /update password/i }));
    expect(await screen.findByText(/password too short/i)).toBeInTheDocument();
  });
});
