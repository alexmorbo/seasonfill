import { describe, it, expect, vi } from 'vitest';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { TooltipProvider } from '@/components/ui/tooltip';
import { QueueRow } from './QueueRow';
import type { MissingSeries } from '@/lib/missing';

function withTooltip(ui: React.ReactElement) {
  return (
    <TooltipProvider delayDuration={0}>{ui}</TooltipProvider>
  );
}

const row: MissingSeries = {
  series_id: 122,
  title: 'Severance',
  title_slug: 'severance',
  year: 2022,
  monitored: true,
  total_missing_aired: 8,
  seasons: [
    { season_number: 2, missing_aired_count: 8, aired_episode_count: 10 },
    { season_number: 3, missing_aired_count: 0, aired_episode_count: 0 },
  ],
};

describe('<QueueRow />', () => {
  it('renders the small poster img pointing at the proxy endpoint', () => {
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    ));
    const img = screen.getByTestId('series-poster-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe(
      '/api/v1/instances/alpha/series/122/poster?size=small',
    );
  });

  it('renders the title, year, missing pill, and season chips', () => {
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    ));
    expect(screen.getByText('Severance')).toBeInTheDocument();
    expect(screen.getByTestId('queue-row-missing-pill')).toHaveTextContent(/8 missing/i);
    const seasons = within(screen.getByTestId('queue-row-seasons'));
    expect(seasons.getByText(/S02/)).toBeInTheDocument();
    expect(seasons.getByText(/S03/)).toBeInTheDocument();
  });

  it('annotates each season chip with present-count and aired-count attributes', () => {
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    ));
    // S2: aired=10, missing=8 → present=2.
    const s2 = screen.getByLabelText(/Season 2: 8 missing/i);
    expect(s2.getAttribute('data-aired-count')).toBe('10');
    expect(s2.getAttribute('data-present-count')).toBe('2');
    // S3: aired=0 → present=0 (no ratio data emitted but the chip still
    // renders for parity with pre-embed shapes).
    const s3 = screen.getByLabelText(/Season 3: 0 missing/i);
    expect(s3.getAttribute('data-aired-count')).toBe('0');
    expect(s3.getAttribute('data-present-count')).toBe('0');
  });

  it('fires onSeasonToggle with the clicked season number', async () => {
    const onSeasonToggle = vi.fn();
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={onSeasonToggle}
        onScan={vi.fn()}
      />,
    ));
    await userEvent.click(screen.getByLabelText(/Season 2: 8 missing/i));
    expect(onSeasonToggle).toHaveBeenCalledWith(2);
  });

  it('marks the active season chip via aria-pressed', () => {
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={2}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    ));
    expect(screen.getByLabelText(/Season 2: 8 missing/i))
      .toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByLabelText(/Season 3: 0 missing/i))
      .toHaveAttribute('aria-pressed', 'false');
  });

  it('renders a single drill panel at the bottom of the row when openSeason is set', () => {
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={2}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        drillSlot={<span>drill-placeholder</span>}
      />,
    ));
    const slot = screen.getByTestId('queue-drill-slot');
    expect(slot).toHaveAttribute('data-season-number', '2');
    expect(slot).toHaveTextContent('drill-placeholder');
    // The drill panel must NOT be nested inside any specific season's
    // list item — it lives at the bottom of the series row.
    const seasonItem = slot.closest('[data-testid="queue-row-season"]');
    expect(seasonItem).toBeNull();
  });

  it('hides the drill panel when openSeason is null', () => {
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        drillSlot={<span>drill-placeholder</span>}
      />,
    ));
    expect(screen.queryByTestId('queue-drill-slot')).not.toBeInTheDocument();
  });

  it('renders per-episode chip grid when season.episodes is provided', () => {
    const rowWithEpisodes: MissingSeries = {
      ...row,
      seasons: [{
        season_number: 2,
        missing_aired_count: 2,
        aired_episode_count: 3,
        episodes: [
          { number: 1, title: 'Good Parts Version', present: true },
          { number: 2, title: 'Trojan Horse', present: false },
          { number: 3, title: 'Hello, Ms. Cobel', present: false },
        ],
      }],
    };
    renderWithProviders(withTooltip(
      <QueueRow
        row={rowWithEpisodes}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    ));
    const grid = screen.getByTestId('queue-season-chips');
    expect(grid.getAttribute('data-season-number')).toBe('2');
    expect(within(grid).getByText('E1').getAttribute('data-present')).toBe('true');
    expect(within(grid).getByText('E2').getAttribute('data-present')).toBe('false');
    expect(within(grid).getByText('E3').getAttribute('data-present')).toBe('false');
  });

  it('omits per-episode chip grid when season.episodes is absent', () => {
    renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    ));
    expect(screen.queryByTestId('queue-season-chips')).toBeNull();
    // The aggregate season pills still render so the drill remains
    // reachable for large seasons that bypass the embed.
    expect(screen.getByLabelText(/Season 2: 8 missing/i)).toBeInTheDocument();
  });

  it('fires onScan and disables the button when in-flight', async () => {
    const onScan = vi.fn();
    const { rerender } = renderWithProviders(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={onScan}
      />,
    ));
    await userEvent.click(screen.getByRole('button', { name: /scan severance now/i }));
    expect(onScan).toHaveBeenCalledTimes(1);
    rerender(withTooltip(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight
        onSeasonToggle={vi.fn()}
        onScan={onScan}
      />,
    ));
    expect(screen.getByRole('button', { name: /scan severance now/i })).toBeDisabled();
  });
});
