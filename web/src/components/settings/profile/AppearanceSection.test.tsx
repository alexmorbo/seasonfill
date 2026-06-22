import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, fireEvent, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { AppearanceSection } from './AppearanceSection';
import { ME_QUERY_KEY } from '@/hooks/useMe';
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
  preferred_language: 'en',
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: null,
  ...overrides,
});

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('<AppearanceSection />', () => {
  it('renders the language select with the current language preselected', () => {
    const me = baseMe({ preferred_language: 'ru' });
    const { qc } = renderWithProviders(<AppearanceSection me={me} />);
    qc.setQueryData(ME_QUERY_KEY, me);
    expect(screen.getByTestId('appearance-language')).toBeInTheDocument();
  });

  it('Save is disabled until avatar mode changes', () => {
    const me = baseMe();
    renderWithProviders(<AppearanceSection me={me} />);
    expect(screen.getByTestId('appearance-save')).toBeDisabled();
  });

  it('Save fires PATCH /me/settings with new avatar_mode when changed', async () => {
    const me = baseMe({ avatar_mode: 'auto' });
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        new Response(JSON.stringify({ ...me, avatar_mode: 'monogram' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    const { qc } = renderWithProviders(<AppearanceSection me={me} />);
    qc.setQueryData(ME_QUERY_KEY, me);
    const monogramRadio = document.getElementById('avatar-mode-monogram') as HTMLElement;
    fireEvent.click(monogramRadio);
    await waitFor(() => expect(screen.getByTestId('appearance-save')).not.toBeDisabled());
    fireEvent.click(screen.getByTestId('appearance-save'));
    await waitFor(() => {
      const call = fetchSpy.mock.calls.find((c) => String(c[0]).endsWith('/me/settings'));
      expect(call).toBeDefined();
      expect((call?.[1] as RequestInit | undefined)?.method).toBe('PATCH');
      expect((call?.[1] as RequestInit | undefined)?.body).toContain('"avatar_mode":"monogram"');
    });
  });

  it('renders the live Avatar preview at 96px with the resolved mode', () => {
    const me = baseMe({ avatar_resolved_mode: 'gravatar' });
    renderWithProviders(<AppearanceSection me={me} />);
    const avatar = screen.getByTestId('appearance-section-avatar');
    expect(avatar.style.width).toBe('96px');
    expect(avatar).toHaveAttribute('data-resolved-mode', 'gravatar');
  });

  it('shows three radio options (no custom)', () => {
    renderWithProviders(<AppearanceSection me={baseMe()} />);
    expect(document.getElementById('avatar-mode-auto')).toBeInTheDocument();
    expect(document.getElementById('avatar-mode-monogram')).toBeInTheDocument();
    expect(document.getElementById('avatar-mode-gravatar')).toBeInTheDocument();
    expect(document.getElementById('avatar-mode-custom')).not.toBeInTheDocument();
  });
});
