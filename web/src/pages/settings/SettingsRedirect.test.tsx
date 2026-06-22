import { describe, it, expect, vi, beforeEach } from 'vitest';
import { Routes, Route } from 'react-router-dom';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import * as meModule from '@/hooks/useMe';
import { SettingsRedirect } from './SettingsRedirect';
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
  preferred_language: null,
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: null,
  ...overrides,
});

beforeEach(() => {
  vi.restoreAllMocks();
});

function setup() {
  return renderWithProviders(
    <Routes>
      <Route path="/settings" element={<SettingsRedirect />} />
      <Route path="/settings/system/general" element={<div data-testid="lp-system">SYSTEM</div>} />
      <Route path="/settings/profile" element={<div data-testid="lp-profile">PROFILE</div>} />
    </Routes>,
    { route: '/settings' },
  );
}

describe('<SettingsRedirect />', () => {
  it('routes admin to /settings/system/general', async () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: baseMe({ role: 'admin' }),
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    setup();
    await waitFor(() => expect(screen.getByTestId('lp-system')).toBeInTheDocument());
  });

  it('routes non-admin to /settings/profile', async () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: baseMe({ role: 'user' }),
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    setup();
    await waitFor(() => expect(screen.getByTestId('lp-profile')).toBeInTheDocument());
  });

  it('renders a loading state while /me is in-flight', () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    setup();
    expect(screen.getByText(/checking|сессия/i)).toBeInTheDocument();
  });

  it('routes to /settings/profile when /me errors (before global 401 redirect)', async () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('boom'),
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    setup();
    await waitFor(() => expect(screen.getByTestId('lp-profile')).toBeInTheDocument());
  });
});
