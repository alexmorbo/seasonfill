import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { Settings } from './Settings';

const origFetch = globalThis.fetch;
beforeEach(() => {
  globalThis.fetch = vi.fn(async () =>
    new Response(JSON.stringify({ instances: [] }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }),
  ) as typeof fetch;
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/settings', hash: '', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<Settings />', () => {
  it('renders three tab triggers', async () => {
    renderWithProviders(<Settings />, { route: '/settings' });
    expect(await screen.findByRole('tab', { name: /instances/i })).toBeVisible();
    expect(screen.getByRole('tab', { name: /general/i })).toBeVisible();
    expect(screen.getByRole('tab', { name: /security/i })).toBeVisible();
  });

  it('default tab is Instances', async () => {
    renderWithProviders(<Settings />, { route: '/settings' });
    expect(await screen.findByRole('button', { name: /add instance/i })).toBeVisible();
  });

  it('switches to General tab on click', async () => {
    renderWithProviders(<Settings />, { route: '/settings' });
    await userEvent.click(screen.getByRole('tab', { name: /general/i }));
    await waitFor(() =>
      expect(screen.getByText(/delivered by story 027e-2/i)).toBeVisible(),
    );
  });
});
