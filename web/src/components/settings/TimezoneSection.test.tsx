import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, fireEvent, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { TimezoneProvider } from '@/lib/timezone';
import { TimezoneSection } from './TimezoneSection';

beforeEach(() => {
  vi.restoreAllMocks();
});

function mockFetch(state: { timezone: string; source: string; requires_restart?: boolean }) {
  return vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify(state), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
}

function renderSection() {
  return renderWithProviders(
    <TimezoneProvider>
      <TimezoneSection />
    </TimezoneProvider>,
  );
}

describe('TimezoneSection', () => {
  it('renders the current zone from the API and the source pill', async () => {
    mockFetch({ timezone: 'Europe/Moscow', source: 'env', requires_restart: false });
    renderSection();
    await waitFor(() => {
      expect(screen.getByTestId('timezone-source-pill')).toBeInTheDocument();
    });
  });

  it('Save button stays disabled until the user picks a new zone', async () => {
    mockFetch({ timezone: 'UTC', source: 'default', requires_restart: false });
    renderSection();
    const save = await screen.findByTestId('timezone-save-button');
    expect(save).toBeDisabled();
  });

  it('shows the restart banner once requires_restart=true', async () => {
    mockFetch({ timezone: 'America/New_York', source: 'db', requires_restart: true });
    renderSection();
    await waitFor(() => {
      expect(screen.getByTestId('timezone-restart-banner')).toBeInTheDocument();
    });
  });

  it('clicking dismiss hides the restart banner for the rest of the session', async () => {
    mockFetch({ timezone: 'America/New_York', source: 'db', requires_restart: true });
    renderSection();
    const banner = await screen.findByTestId('timezone-restart-banner');
    const dismiss = banner.querySelector('button');
    expect(dismiss).not.toBeNull();
    fireEvent.click(dismiss as HTMLElement);
    await waitFor(() => {
      expect(screen.queryByTestId('timezone-restart-banner')).not.toBeInTheDocument();
    });
  });
});
