import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { TopBar } from './TopBar';
import { InstanceFilterProvider } from '@/lib/instance-filter-context';
import * as auth from '@/lib/auth';

const { navigateSpy, toastSpies } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  toastSpies: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}));
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigateSpy };
});
vi.mock('sonner', () => ({ toast: toastSpies }));

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/', search: '', assign: vi.fn() },
  });
  globalThis.fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
    instances: [
      { name: 'alpha', health: 'available' },
      { name: 'beta',  health: 'degraded' },
    ],
  }), { status: 200, headers: { 'Content-Type': 'application/json' } })) as typeof fetch;
});
afterEach(() => {
  vi.restoreAllMocks();
  navigateSpy.mockReset(); toastSpies.success.mockReset(); toastSpies.error.mockReset();
});

const ui = () => (
  <InstanceFilterProvider><TopBar onMenuClick={() => {}} /></InstanceFilterProvider>
);

describe('<TopBar />', () => {
  it('renders one chip per instance from useInstances()', async () => {
    renderWithProviders(ui());
    expect(await screen.findByTitle('Filter by alpha')).toBeInTheDocument();
    expect(screen.getByTitle('Filter by beta')).toBeInTheDocument();
  });

  it('toggles aria-pressed when chip is clicked', async () => {
    renderWithProviders(ui());
    const chip = await screen.findByTitle('Filter by alpha');
    expect(chip).toHaveAttribute('aria-pressed', 'false');
    await userEvent.click(chip);
    expect(chip).toHaveAttribute('aria-pressed', 'true');
  });

  it('logout calls auth.logout() and navigates to /login', async () => {
    const spy = vi.spyOn(auth, 'logout').mockResolvedValue(undefined);
    renderWithProviders(ui());
    await userEvent.click(screen.getByRole('button', { name: /account menu/i }));
    await userEvent.click(await screen.findByRole('menuitem', { name: /logout/i }));
    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(toastSpies.success).toHaveBeenCalledWith('Signed out');
    expect(navigateSpy).toHaveBeenCalledWith('/login', { replace: true });
  });
});
