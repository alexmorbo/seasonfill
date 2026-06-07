import { describe, expect, it, vi, beforeEach } from 'vitest';
import { screen, waitFor, within } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { renderWithProviders } from '@/test-utils';
import { WatchdogBlacklistTable } from './WatchdogBlacklistTable';
import {
  watchdogBlacklistKey, type WatchdogBlacklistList,
} from '@/lib/api/watchdogBlacklist';

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: vi.fn() };
});

import { api } from '@/lib/api';

const fixture: WatchdogBlacklistList = {
  items: [
    {
      id: 1, instance_name: 'homelab', series_id: 100,
      series_title: 'Wednesday', season_number: 2,
      reason: 'consecutive_no_better', source: 'auto', consecutive: 3,
      created_at: new Date(Date.now() - 60_000).toISOString(),
    },
    {
      id: 2, instance_name: 'homelab', series_id: 101,
      series_title: 'Black Mirror', season_number: 6,
      reason: 'manual', source: 'manual', consecutive: 0,
      created_at: new Date(Date.now() - 3 * 86_400_000).toISOString(),
    },
  ],
};

function wrap(ui: React.ReactNode) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('<WatchdogBlacklistTable />', () => {
  beforeEach(() => { vi.mocked(api).mockReset(); });

  it('renders header + rows when data resolves', async () => {
    vi.mocked(api).mockResolvedValue(fixture);
    renderWithProviders(
      wrap(<WatchdogBlacklistTable instance="homelab" maxNoBetter={3} />),
    );
    await waitFor(() =>
      expect(screen.getByTestId('watchdog-blacklist-header')).toBeInTheDocument(),
    );
    expect(screen.getByTestId('watchdog-blacklist-row-1')).toBeInTheDocument();
    expect(screen.getByTestId('watchdog-blacklist-row-2')).toBeInTheDocument();
    expect(screen.getByText('Wednesday')).toBeInTheDocument();
    expect(screen.getByText('Black Mirror')).toBeInTheDocument();
    expect(screen.getByText('S02')).toBeInTheDocument();
    expect(screen.getByText('S06')).toBeInTheDocument();
  });

  it('renders the empty branch when items is empty', async () => {
    vi.mocked(api).mockResolvedValue({ items: [] });
    renderWithProviders(wrap(<WatchdogBlacklistTable instance="homelab" />));
    await waitFor(() =>
      expect(screen.getByTestId('watchdog-blacklist-empty')).toBeInTheDocument(),
    );
  });

  it('renders the skeleton while loading', () => {
    vi.mocked(api).mockImplementation(() => new Promise(() => {}));
    renderWithProviders(wrap(<WatchdogBlacklistTable instance="homelab" />));
    expect(screen.getByTestId('watchdog-blacklist-loading')).toBeInTheDocument();
  });

  it('renders auto/manual reason badges distinctly', async () => {
    vi.mocked(api).mockResolvedValue(fixture);
    const { qc } = renderWithProviders(
      wrap(<WatchdogBlacklistTable instance="homelab" maxNoBetter={3} />),
    );
    qc.setQueryData(watchdogBlacklistKey('homelab'), fixture);
    await waitFor(() =>
      expect(screen.getByTestId('watchdog-blacklist-row-1')).toBeInTheDocument(),
    );
    const row1 = within(screen.getByTestId('watchdog-blacklist-row-1'));
    const row2 = within(screen.getByTestId('watchdog-blacklist-row-2'));
    expect(row1.getByText(/auto/i)).toBeInTheDocument();
    expect(row2.getByText(/manual/i)).toBeInTheDocument();
  });
});
