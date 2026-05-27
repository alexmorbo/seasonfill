import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { Settings } from './Settings';

const origFetch = globalThis.fetch;

beforeEach(() => {
  globalThis.fetch = vi.fn(async () =>
    new Response(
      JSON.stringify({
        schedule: { enabled: true, expression: '0 * * * *' },
        scan: {},
        defaults: {},
        instances: [],
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    ),
  ) as typeof fetch;
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('<Settings />', () => {
  it('renders two tab triggers (General + Security)', async () => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/settings', hash: '', assign: vi.fn() },
    });
    renderWithProviders(<Settings />, { route: '/settings' });
    expect(await screen.findByRole('tab', { name: /general/i })).toBeVisible();
    expect(screen.getByRole('tab', { name: /security/i })).toBeVisible();
    expect(screen.queryByRole('tab', { name: /^instances$/i })).toBeNull();
  });

  it('default tab is General (Instances tab removed)', async () => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/settings', hash: '', assign: vi.fn() },
    });
    renderWithProviders(<Settings />, { route: '/settings' });
    await waitFor(() =>
      expect(screen.getByLabelText(/cron expression/i)).toBeVisible(),
    );
  });

  it('switches to Security tab on click', async () => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/settings', hash: '', assign: vi.fn() },
    });
    renderWithProviders(<Settings />, { route: '/settings' });
    await userEvent.click(screen.getByRole('tab', { name: /security/i }));
    await waitFor(() =>
      expect(screen.getByText(/session ttl/i)).toBeVisible(),
    );
  });

  it('renders Settings subtitle when legacy /settings#instances hash is detected', async () => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/settings', hash: '#instances', assign: vi.fn() },
    });
    renderWithProviders(<Settings />, { route: '/settings#instances' });
    // Verify the component renders the subtitle text that describes the page.
    await waitFor(() => {
      expect(screen.queryByText(/Manage Sonarr instances/i)).toBeInTheDocument();
    });
  });
});
