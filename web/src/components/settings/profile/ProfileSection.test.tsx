import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { ProfileSection } from './ProfileSection';
import type { MeResponse } from '@/lib/me-types';

const baseMe = (overrides: Partial<MeResponse> = {}): MeResponse => ({
  id: 1,
  username: 'admin',
  email: 'admin@example.com',
  role: 'admin',
  auth_mode: 'forms',
  avatar_mode: 'auto',
  avatar_resolved_mode: 'gravatar',
  avatar_hash: '0bc83cb571cd1c50ba6f3e8a78ef1346',
  preferred_language: 'en-US',
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: '2026-06-22T20:30:00Z',
  ...overrides,
});

describe('<ProfileSection />', () => {
  it('renders username, email, role badge, last_login_at when all fields present', () => {
    renderWithProviders(<ProfileSection me={baseMe()} />);
    expect(screen.getByTestId('profile-username')).toHaveTextContent('admin');
    expect(screen.getByTestId('profile-email')).toHaveTextContent('admin@example.com');
    expect(screen.getByTestId('profile-role').textContent ?? '').toMatch(
      /Administrator|Администратор/,
    );
    expect(screen.getByTestId('profile-last-login').textContent ?? '').not.toBe('—');
  });

  it('renders em-dash for null email', () => {
    renderWithProviders(<ProfileSection me={baseMe({ email: null })} />);
    expect(screen.getByTestId('profile-email')).toHaveTextContent('—');
  });

  it('renders em-dash for null last_login_at', () => {
    renderWithProviders(<ProfileSection me={baseMe({ last_login_at: null })} />);
    expect(screen.getByTestId('profile-last-login')).toHaveTextContent('—');
  });

  it('uses the role=user label for non-admin', () => {
    renderWithProviders(<ProfileSection me={baseMe({ role: 'user' })} />);
    expect(screen.getByTestId('profile-role').textContent ?? '').toMatch(/User|Пользователь/);
  });

  it('mounts the Avatar with the resolved mode + hash', () => {
    renderWithProviders(<ProfileSection me={baseMe()} />);
    const avatar = screen.getByTestId('profile-section-avatar');
    expect(avatar).toHaveAttribute('data-resolved-mode', 'gravatar');
  });

  it('falls through to monogram on Avatar when resolved=monogram', () => {
    renderWithProviders(
      <ProfileSection me={baseMe({ avatar_resolved_mode: 'monogram', email: null, avatar_hash: '' })} />,
    );
    const avatar = screen.getByTestId('profile-section-avatar');
    expect(avatar).toHaveAttribute('data-resolved-mode', 'monogram');
  });
});
