import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { Routes, Route } from 'react-router-dom';
import { screen, waitFor, act } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import * as meModule from '@/hooks/useMe';
import { SystemTabGuard } from './SystemTabGuard';
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
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.restoreAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
});

function setup() {
  return renderWithProviders(
    <Routes>
      <Route
        path="/settings/system/general"
        element={
          <SystemTabGuard>
            <div data-testid="lp-system-content">SYSTEM</div>
          </SystemTabGuard>
        }
      />
      <Route path="/settings/profile" element={<div data-testid="lp-profile">PROFILE</div>} />
    </Routes>,
    { route: '/settings/system/general' },
  );
}

describe('<SystemTabGuard />', () => {
  it('renders children when role=admin', () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: baseMe({ role: 'admin' }),
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    setup();
    expect(screen.getByTestId('lp-system-content')).toBeInTheDocument();
  });

  it('renders the 403 panel and auto-redirects after 2s when role=user', async () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: baseMe({ role: 'user' }),
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    setup();
    expect(screen.getByTestId('system-tab-guard-denied')).toBeInTheDocument();
    expect(screen.queryByTestId('lp-system-content')).not.toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    await waitFor(() => expect(screen.getByTestId('lp-profile')).toBeInTheDocument());
  });

  it('renders loading placeholder while /me is in-flight', () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    setup();
    expect(screen.queryByTestId('lp-system-content')).not.toBeInTheDocument();
    expect(screen.queryByTestId('system-tab-guard-denied')).not.toBeInTheDocument();
    expect(screen.getByText(/checking|сессия/i)).toBeInTheDocument();
  });

  it('clears the redirect timer on unmount (no late navigate)', async () => {
    vi.spyOn(meModule, 'useMe').mockReturnValue({
      data: baseMe({ role: 'user' }),
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof meModule.useMe>);
    const { unmount } = setup();
    unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    // The redirect should NOT have fired because the timer was cleared.
    expect(screen.queryByTestId('lp-profile')).not.toBeInTheDocument();
  });
});
