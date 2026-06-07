import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DashboardEmptyState } from './DashboardEmptyState';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

const lastImportFixture: SeriesCacheItem = {
  sonarr_series_id: 1,
  instance_name: 'alpha',
  title: 'Breaking Bad',
  title_slug: 'breaking-bad',
  year: 2008,
  network: 'AMC',
  status: 'ended',
  poster_path: '/path/to/poster.jpg',
  monitored: true,
  missing_count: 0,
  last_grab_at: new Date(Date.now() - 86400000).toISOString(),
  last_imported_episode: 'S05E16',
  updated_at: new Date().toISOString(),
};

function renderEmptyState(
  missingCount: number | null = 0,
  lastImport: SeriesCacheItem | null = null,
  scanPending = false,
  onScanNow = vi.fn(),
  onOpenQueue = vi.fn(),
) {
  return {
    onScanNow,
    onOpenQueue,
    ...render(
      <I18nextProvider i18n={i18n}>
        <DashboardEmptyState
          missingCount={missingCount}
          lastImport={lastImport}
          onScanNow={onScanNow}
          onOpenQueue={onOpenQueue}
          scanPending={scanPending}
        />
      </I18nextProvider>,
    ),
  };
}

describe('<DashboardEmptyState />', () => {
  it('renders Moon icon, title, body text', () => {
    renderEmptyState();
    expect(screen.getByTestId('dashboard-empty-state')).toBeInTheDocument();
    expect(screen.getByText(/quiet today/i)).toBeInTheDocument();
    expect(screen.getByText(/hasn't pulled anything/i)).toBeInTheDocument();
  });

  it('renders Scan CTA button and calls onScanNow on click', async () => {
    const { onScanNow } = renderEmptyState();
    const user = userEvent.setup();
    const scanBtn = screen.getByTestId('empty-cta-scan');
    expect(scanBtn).toBeInTheDocument();
    await user.click(scanBtn);
    expect(onScanNow).toHaveBeenCalledOnce();
  });

  it('disables Scan CTA when scanPending=true', () => {
    renderEmptyState(0, null, true);
    const scanBtn = screen.getByTestId('empty-cta-scan');
    expect(scanBtn).toBeDisabled();
  });

  it('renders Queue CTA with count when missingCount !== null', () => {
    renderEmptyState(8);
    const queueBtn = screen.getByTestId('empty-cta-queue');
    expect(queueBtn).toBeInTheDocument();
    expect(queueBtn).toHaveTextContent(/8/);
  });

  it('calls onOpenQueue when Queue CTA clicked', async () => {
    const { onOpenQueue } = renderEmptyState(5);
    const user = userEvent.setup();
    await user.click(screen.getByTestId('empty-cta-queue'));
    expect(onOpenQueue).toHaveBeenCalledOnce();
  });

  it('does not render Queue CTA when missingCount is null', () => {
    renderEmptyState(null);
    expect(screen.queryByTestId('empty-cta-queue')).not.toBeInTheDocument();
  });

  it('renders last-import footer when lastImport is provided', () => {
    renderEmptyState(0, lastImportFixture);
    expect(screen.getByText(/Breaking Bad/)).toBeInTheDocument();
    expect(screen.getByText(/S05E16|S5E16/)).toBeInTheDocument();
  });

  it('does not render last-import footer when lastImport is null', () => {
    renderEmptyState(0, null);
    expect(screen.queryByText(/Breaking Bad/)).not.toBeInTheDocument();
  });
});
