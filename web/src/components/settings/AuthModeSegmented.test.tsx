import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { AuthModeSegmented } from './AuthModeSegmented';

function renderControl(props: Partial<Parameters<typeof AuthModeSegmented>[0]> = {}) {
  const onAttempt = vi.fn();
  render(
    <I18nextProvider i18n={i18n}>
      <AuthModeSegmented current={props.current ?? 'forms'} onAttempt={props.onAttempt ?? onAttempt} />
    </I18nextProvider>,
  );
  return { onAttempt };
}

describe('<AuthModeSegmented />', () => {
  it('renders the 4 mode buttons with data-mode attributes', () => {
    renderControl();
    expect(screen.getByRole('radio', { name: 'Forms' })).toHaveAttribute('data-mode', 'forms');
    expect(screen.getByRole('radio', { name: 'Basic' })).toHaveAttribute('data-mode', 'basic');
    expect(screen.getByRole('radio', { name: 'None'  })).toHaveAttribute('data-mode', 'none');
    expect(screen.getByRole('radio', { name: 'OIDC'  })).toHaveAttribute('data-mode', 'oidc');
  });

  it('highlights the current mode via aria-checked', () => {
    renderControl({ current: 'oidc' });
    expect(screen.getByRole('radio', { name: 'OIDC' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('radio', { name: 'Forms' })).toHaveAttribute('aria-checked', 'false');
  });

  it('clicking the current mode is a no-op', async () => {
    const onAttempt = vi.fn();
    renderControl({ current: 'forms', onAttempt });
    await userEvent.click(screen.getByRole('radio', { name: 'Forms' }));
    expect(onAttempt).not.toHaveBeenCalled();
  });

  it('clicking a non-current mode fires onAttempt(target)', async () => {
    const onAttempt = vi.fn();
    renderControl({ current: 'forms', onAttempt });
    await userEvent.click(screen.getByRole('radio', { name: 'OIDC' }));
    expect(onAttempt).toHaveBeenCalledWith('oidc');
  });

  it('renders the current-mode pill with interpolation', () => {
    renderControl({ current: 'basic' });
    const pill = screen.getByTestId('auth-mode-pill');
    expect(pill.textContent).toMatch(/basic/i);
  });

  it('renders the danger-note copy', () => {
    renderControl();
    // Danger-note head copy is i18n-driven; assert the rescue command code appears.
    expect(screen.getByText(/seasonfill auth-mode/i)).toBeInTheDocument();
  });
});
