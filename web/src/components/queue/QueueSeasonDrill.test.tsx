import { describe, it, expect, vi, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { TooltipProvider } from '@/components/ui/tooltip';
import { QueueSeasonDrill } from './QueueSeasonDrill';

function withTooltip(ui: React.ReactElement) {
  return <TooltipProvider delayDuration={0}>{ui}</TooltipProvider>;
}

const origFetch = globalThis.fetch;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });

afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<QueueSeasonDrill />', () => {
  it('renders loading then episodes', async () => {
    globalThis.fetch = vi.fn(async (url) => {
      const path = typeof url === 'string' ? url : url.toString();
      if (path.includes('/series/122/seasons/2/episodes')) {
        return json({
          items: [
            { number: 1, monitored: true, has_file: true, aired: true, air_date_utc: '2024-01-01T00:00:00Z' },
            { number: 2, monitored: true, has_file: false, aired: true, air_date_utc: '2024-01-08T00:00:00Z' },
          ],
          total: 2, have: 1, miss: 1,
        });
      }
      return json({});
    }) as typeof fetch;

    renderWithProviders(withTooltip(
      <QueueSeasonDrill
        instanceName="alpha"
        seriesId={122}
        seasonNumber={2}
        isScanInFlight={false}
        onScanSeason={vi.fn()}
      />,
    ));
    expect(screen.getByTestId('queue-drill-loading')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId('queue-drill')).toBeInTheDocument());
    expect(screen.getByText(/S2 — missing 1 of 2/i)).toBeInTheDocument();
    expect(screen.getByTestId('queue-avail-bar')).toBeInTheDocument();
  });

  it('renders error state on fetch failure', async () => {
    globalThis.fetch = vi.fn(async () => json({ error: 'boom' }, 502)) as typeof fetch;
    renderWithProviders(withTooltip(
      <QueueSeasonDrill
        instanceName="alpha"
        seriesId={122}
        seasonNumber={2}
        isScanInFlight={false}
        onScanSeason={vi.fn()}
      />,
    ));
    expect(await screen.findByTestId('queue-drill-error')).toBeInTheDocument();
  });

  it('fires onScanSeason and shows the disclosure tooltip', async () => {
    globalThis.fetch = vi.fn(async () =>
      json({
        items: [{ number: 1, monitored: true, has_file: false, aired: true, air_date_utc: '2024-01-01T00:00:00Z' }],
        total: 1, have: 0, miss: 1,
      }),
    ) as typeof fetch;
    const onScanSeason = vi.fn();
    renderWithProviders(withTooltip(
      <QueueSeasonDrill
        instanceName="alpha"
        seriesId={122}
        seasonNumber={5}
        isScanInFlight={false}
        onScanSeason={onScanSeason}
      />,
    ));
    const btn = await screen.findByTestId('queue-drill-scan-season');
    expect(btn).toHaveAttribute('title', expect.stringMatching(/per-season targeting/i));
    await userEvent.click(btn);
    expect(onScanSeason).toHaveBeenCalledTimes(1);
  });
});
