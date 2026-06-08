import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { OIDCFold } from './OIDCFold';
import type { OIDCFormShape } from './OIDCConfigBlock';

const VALUE: OIDCFormShape & { client_secret_configured: boolean; client_secret_env_override: boolean } = {
  issuer: '',
  client_id: '',
  redirect_url: '',
  scopes: ['openid', 'profile', 'email'],
  username_claim: 'preferred_username',
  allowed_groups: [],
  groups_claim: 'groups',
  client_secret_configured: false,
  client_secret_env_override: false,
};

function renderFold(props: Partial<Parameters<typeof OIDCFold>[0]> = {}) {
  return render(
    <I18nextProvider i18n={i18n}>
      <OIDCFold
        mode={props.mode ?? 'forms'}
        forceOpen={props.forceOpen ?? false}
        value={props.value ?? VALUE}
        onChange={props.onChange ?? (() => {})}
        onTest={props.onTest ?? vi.fn().mockResolvedValue({ ok: true })}
        {...(props.errors && { errors: props.errors })}
      />
    </I18nextProvider>,
  );
}

describe('<OIDCFold />', () => {
  it('auto-opens when mode is oidc', () => {
    renderFold({ mode: 'oidc' });
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'true');
    // Inner OIDCConfigBlock fields show through.
    expect(screen.getByLabelText(/issuer/i)).toBeVisible();
  });

  it('starts collapsed in non-oidc modes', () => {
    renderFold({ mode: 'forms' });
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'false');
  });

  it('clicking the header toggles open in non-oidc mode', async () => {
    renderFold({ mode: 'forms' });
    await userEvent.click(screen.getByTestId('oidc-fold-head'));
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'true');
  });

  it('forceOpen=true keeps it open and disables the toggle', async () => {
    renderFold({ mode: 'forms', forceOpen: true });
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'true');
    await userEvent.click(screen.getByTestId('oidc-fold-head'));
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'true');
  });

  it('user can collapse the fold even when mode is oidc', async () => {
    renderFold({ mode: 'oidc' });
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'true');
    await userEvent.click(screen.getByTestId('oidc-fold-head'));
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'false');
    await userEvent.click(screen.getByTestId('oidc-fold-head'));
    expect(screen.getByTestId('oidc-fold')).toHaveAttribute('data-open', 'true');
  });

  it('open content carries data-state=open and collapsible-down animation class', () => {
    renderFold({ mode: 'oidc' });
    const content = screen.getByTestId('oidc-fold-content');
    expect(content).toHaveAttribute('data-state', 'open');
    expect(content.className).toMatch(/data-\[state=open\]:animate-collapsible-down/);
    expect(content.className).toMatch(/data-\[state=closed\]:animate-collapsible-up/);
    expect(content.className).toMatch(/overflow-hidden/);
  });

  it("closed content stays in DOM with hidden + data-state='closed' (Radix default)", () => {
    renderFold({ mode: 'forms' });
    const content = screen.getByTestId('oidc-fold-content');
    expect(content).toHaveAttribute('data-state', 'closed');
    expect(content).toHaveAttribute('hidden');
  });
});
