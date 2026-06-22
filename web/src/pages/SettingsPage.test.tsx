import { describe, it, expect, vi, beforeEach } from 'vitest';
import { Routes, Route } from 'react-router-dom';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import * as meModule from '@/hooks/useMe';
import { SettingsPage } from './SettingsPage';
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
  vi.spyOn(meModule, 'useMe').mockReturnValue({
    data: baseMe({ role: 'admin' }),
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof meModule.useMe>);
});

function setup(initialEntries: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/settings" element={<SettingsPage />}>
        <Route index element={<div data-testid="lp-index">INDEX</div>} />
        <Route path="profile" element={<div data-testid="lp-profile">PROFILE</div>} />
        <Route path="system/general" element={<div data-testid="lp-general">GENERAL</div>} />
        <Route path="system/security" element={<div data-testid="lp-security">SECURITY</div>} />
        <Route path="system/integrations" element={<div data-testid="lp-integrations">INTEGRATIONS</div>} />
      </Route>
      <Route path="/instances" element={<div data-testid="lp-instances">INSTANCES</div>} />
    </Routes>,
    { route: initialEntries },
  );
}

describe('<SettingsPage /> legacy hash migration', () => {
  it('redirects #general → /settings/system/general', async () => {
    // MemoryRouter ignores window.location.hash; emulate via window directly.
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hash: '#general' },
      writable: true,
    });
    setup('/settings');
    await waitFor(() => expect(screen.getByTestId('lp-general')).toBeInTheDocument());
  });

  it('redirects #security → /settings/system/security', async () => {
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hash: '#security' },
      writable: true,
    });
    setup('/settings');
    await waitFor(() => expect(screen.getByTestId('lp-security')).toBeInTheDocument());
  });

  it('redirects #integrations → /settings/system/integrations', async () => {
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hash: '#integrations' },
      writable: true,
    });
    setup('/settings');
    await waitFor(() => expect(screen.getByTestId('lp-integrations')).toBeInTheDocument());
  });

  it('redirects legacy #instances → /instances', async () => {
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hash: '#instances' },
      writable: true,
    });
    setup('/settings');
    await waitFor(() => expect(screen.getByTestId('lp-instances')).toBeInTheDocument());
  });

  it('renders the index Outlet when no hash is present (no re-fire)', () => {
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hash: '' },
      writable: true,
    });
    setup('/settings');
    expect(screen.getByTestId('lp-index')).toBeInTheDocument();
  });
});
