import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, fireEvent, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import i18n from '@/i18n';
import { ChangePasswordForm } from './ChangePasswordForm';

function getCurrentInput(): HTMLInputElement {
  return document.getElementById('cp-current') as HTMLInputElement;
}
function getNewInput(): HTMLInputElement {
  return document.getElementById('cp-new') as HTMLInputElement;
}
function getConfirmInput(): HTMLInputElement {
  return document.getElementById('cp-confirm') as HTMLInputElement;
}

function fillForm(current: string, next: string, confirm: string) {
  fireEvent.input(getCurrentInput(), { target: { value: current } });
  fireEvent.input(getNewInput(), { target: { value: next } });
  fireEvent.input(getConfirmInput(), { target: { value: confirm } });
}

beforeEach(() => {
  vi.restoreAllMocks();
  void i18n.changeLanguage('en');
});

describe('<ChangePasswordForm />', () => {
  it('rejects new_password shorter than 12 chars (client-side)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    renderWithProviders(<ChangePasswordForm />);
    fillForm('current-x', 'short', 'short');
    fireEvent.click(screen.getByTestId('change-password-submit'));
    await waitFor(() => {
      const alerts = screen.getAllByRole('alert');
      expect(alerts.length).toBeGreaterThanOrEqual(1);
    });
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('rejects mismatched confirm', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    renderWithProviders(<ChangePasswordForm />);
    fillForm('current-x', 'long-enough-pw-xx', 'different-pw-xx');
    fireEvent.click(screen.getByTestId('change-password-submit'));
    await waitFor(() => {
      expect(screen.getAllByRole('alert').length).toBeGreaterThanOrEqual(1);
    });
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('submits POST /me/change-password on valid input', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 204 }),
    );
    renderWithProviders(<ChangePasswordForm />);
    fillForm('current-x', 'long-enough-pw-xx', 'long-enough-pw-xx');
    fireEvent.click(screen.getByTestId('change-password-submit'));
    await waitFor(() => {
      const call = fetchSpy.mock.calls.find((c) => String(c[0]).endsWith('/me/change-password'));
      expect(call).toBeDefined();
      expect((call?.[1] as RequestInit | undefined)?.method).toBe('POST');
    });
  });

  it('maps 401 to a current_password field error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'invalid_current_password' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    renderWithProviders(<ChangePasswordForm />);
    fillForm('wrong', 'long-enough-pw-xx', 'long-enough-pw-xx');
    fireEvent.click(screen.getByTestId('change-password-submit'));
    await waitFor(() => {
      const alerts = screen.getAllByRole('alert');
      expect(alerts.some((a) => /incorrect|неверн/i.test(a.textContent ?? ''))).toBe(true);
    });
  });

  it('clears the form on 204 success', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 204 }),
    );
    renderWithProviders(<ChangePasswordForm />);
    fillForm('current-x', 'long-enough-pw-xx', 'long-enough-pw-xx');
    fireEvent.click(screen.getByTestId('change-password-submit'));
    await waitFor(() => {
      expect(getCurrentInput().value).toBe('');
      expect(getNewInput().value).toBe('');
    });
  });

  it('renders the unavailable banner on 405', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          error: 'password_change_unavailable',
          reason: 'managed_by_idp',
          manage_url: null,
        }),
        { status: 405, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    renderWithProviders(<ChangePasswordForm />);
    fillForm('current-x', 'long-enough-pw-xx', 'long-enough-pw-xx');
    fireEvent.click(screen.getByTestId('change-password-submit'));
    await waitFor(() => {
      expect(
        screen.getByText(/not available|недоступн/i),
      ).toBeInTheDocument();
    });
  });
});
