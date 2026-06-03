import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { OIDCConfigBlock, type OIDCFormShape } from './OIDCConfigBlock';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';

function renderWith(value: OIDCFormShape, onChange = vi.fn()) {
  return render(
    <I18nextProvider i18n={i18n}>
      <OIDCConfigBlock value={value} onChange={onChange} />
    </I18nextProvider>,
  );
}

const empty: OIDCFormShape = {
  issuer: '',
  client_id: '',
  redirect_url: '',
  scopes: ['openid'],
  username_claim: 'preferred_username',
  allowed_groups: [],
};

describe('OIDCConfigBlock', () => {
  it('renders the client-secret env hint', () => {
    renderWith(empty);
    expect(screen.getByText(/OIDC_CLIENT_SECRET/i)).toBeInTheDocument();
  });

  it('forwards issuer changes via onChange', () => {
    const onChange = vi.fn();
    renderWith(empty, onChange);
    fireEvent.change(screen.getByLabelText(/Issuer URL/i), {
      target: { value: 'https://k.example.com' },
    });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ issuer: 'https://k.example.com' }),
    );
  });

  it('adds a scope on Enter', () => {
    const onChange = vi.fn();
    renderWith(empty, onChange);
    const scopesInput = screen.getByLabelText(/Add scope/i);
    fireEvent.change(scopesInput, { target: { value: 'profile' } });
    fireEvent.keyDown(scopesInput, { key: 'Enter' });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ scopes: ['openid', 'profile'] }),
    );
  });

  it('shows issuer error when provided', () => {
    render(
      <I18nextProvider i18n={i18n}>
        <OIDCConfigBlock
          value={empty}
          onChange={vi.fn()}
          errors={{ issuer: 'settings.security.oidc.issuer.required' }}
        />
      </I18nextProvider>,
    );
    const alerts = screen.getAllByRole('alert');
    const errorAlert = alerts.find((el) => /Issuer URL is required/i.test(el.textContent ?? ''));
    expect(errorAlert).toBeDefined();
    expect(errorAlert).toHaveTextContent(/Issuer URL is required/i);
  });
});
