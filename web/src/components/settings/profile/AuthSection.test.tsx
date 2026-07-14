import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { AuthSection } from './AuthSection';
import type { MeResponse } from '@/lib/me-types';

const baseMe = (overrides: Partial<MeResponse> = {}): MeResponse => ({
  id: 1,
  username: 'admin',
  email: 'admin@example.com',
  role: 'admin',
  auth_mode: 'forms',
  avatar_mode: 'auto',
  avatar_resolved_mode: 'gravatar',
  avatar_hash: 'abc',
  preferred_language: 'en-US',
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: null,
  ...overrides,
});

describe('<AuthSection />', () => {
  it('renders ChangePasswordForm in forms mode', () => {
    renderWithProviders(<AuthSection me={baseMe({ auth_mode: 'forms' })} />);
    expect(screen.getByTestId('auth-section')).toHaveAttribute('data-auth-mode', 'forms');
    expect(screen.getByTestId('change-password-form')).toBeInTheDocument();
  });

  it('renders the OIDC profile link in oidc mode when idp_profile_url is present', () => {
    renderWithProviders(
      <AuthSection
        me={baseMe({
          auth_mode: 'oidc',
          idp_profile_url: 'https://idp.example.com/account',
        })}
      />,
    );
    const link = screen.getByTestId('oidc-profile-link') as HTMLAnchorElement;
    expect(link.href).toBe('https://idp.example.com/account');
    expect(link.target).toBe('_blank');
    expect(link.rel).toContain('noopener');
    expect(screen.queryByTestId('change-password-form')).not.toBeInTheDocument();
  });

  it('renders disabled notice in oidc mode when idp_profile_url is null', () => {
    renderWithProviders(<AuthSection me={baseMe({ auth_mode: 'oidc' })} />);
    expect(screen.getByTestId('oidc-no-profile-url')).toBeInTheDocument();
    expect(screen.queryByTestId('oidc-profile-link')).not.toBeInTheDocument();
  });
});
