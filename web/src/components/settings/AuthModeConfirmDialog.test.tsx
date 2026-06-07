import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { AuthModeConfirmDialog } from './AuthModeConfirmDialog';

function renderDialog(open = true) {
  const onOpenChange = vi.fn();
  const onConfirm = vi.fn();
  const utils = render(
    <I18nextProvider i18n={i18n}>
      <AuthModeConfirmDialog
        open={open}
        onOpenChange={onOpenChange}
        currentMode="forms"
        targetMode="oidc"
        onConfirm={onConfirm}
      />
    </I18nextProvider>,
  );
  return { onOpenChange, onConfirm, ...utils };
}

describe('<AuthModeConfirmDialog />', () => {
  it('renders title with target mode interpolated', () => {
    renderDialog();
    expect(screen.getByText(/oidc/i)).toBeInTheDocument();
  });

  it('confirm button is disabled until ack is checked', async () => {
    renderDialog();
    const confirm = screen.getByTestId('auth-mode-confirm-confirm') as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    await userEvent.click(screen.getByTestId('auth-mode-confirm-ack'));
    expect(confirm.disabled).toBe(false);
  });

  it('confirm fires onConfirm and closes the dialog', async () => {
    const { onConfirm, onOpenChange } = renderDialog();
    await userEvent.click(screen.getByTestId('auth-mode-confirm-ack'));
    await userEvent.click(screen.getByTestId('auth-mode-confirm-confirm'));
    expect(onConfirm).toHaveBeenCalledOnce();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('cancel closes without firing onConfirm', async () => {
    const { onConfirm, onOpenChange } = renderDialog();
    await userEvent.click(screen.getByTestId('auth-mode-confirm-cancel'));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('re-opening resets the ack state', async () => {
    const { rerender } = renderDialog(true);
    await userEvent.click(screen.getByTestId('auth-mode-confirm-ack'));
    expect((screen.getByTestId('auth-mode-confirm-confirm') as HTMLButtonElement).disabled).toBe(false);

    // Close → reopen
    rerender(
      <I18nextProvider i18n={i18n}>
        <AuthModeConfirmDialog
          open={false} onOpenChange={() => {}}
          currentMode="forms" targetMode="oidc" onConfirm={() => {}}
        />
      </I18nextProvider>,
    );
    rerender(
      <I18nextProvider i18n={i18n}>
        <AuthModeConfirmDialog
          open={true} onOpenChange={() => {}}
          currentMode="forms" targetMode="oidc" onConfirm={() => {}}
        />
      </I18nextProvider>,
    );
    expect((screen.getByTestId('auth-mode-confirm-confirm') as HTMLButtonElement).disabled).toBe(true);
  });
});
