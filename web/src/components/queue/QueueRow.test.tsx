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

const EMPTY = new Set<number>();

const row: MissingSeries = {
  series_id: 122,
  title: 'Severance',
  title_slug: 'severance',
  year: 2022,
  monitored: true,
  total_missing_aired: 8,
  seasons: [
    { season_number: 2, missing_aired_count: 8 },
    { season_number: 3, missing_aired_count: 0 },
  ],
};

describe('<QueueRow />', () => {
  it('renders the small poster img pointing at the proxy endpoint', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={EMPTY}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    );
    const img = screen.getByTestId('series-poster-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe(
      '/api/v1/instances/alpha/series/122/poster?size=small',
    );
  });

  it('renders the title, year, missing pill, and season chips', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={EMPTY}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    );
    expect(screen.getByText('Severance')).toBeInTheDocument();
    expect(screen.getByTestId('queue-row-missing-pill')).toHaveTextContent(/8 missing/i);
    const seasons = within(screen.getByTestId('queue-row-seasons'));
    expect(seasons.getByText(/S02/)).toBeInTheDocument();
    expect(seasons.getByText(/S03/)).toBeInTheDocument();
  });

  it('fires onSeasonToggle with the clicked season number', async () => {
    const onSeasonToggle = vi.fn();
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={EMPTY}
        isInFlight={false}
        onSeasonToggle={onSeasonToggle}
        onScan={vi.fn()}
      />,
    );
    await userEvent.click(screen.getByLabelText(/Season 2: 8 missing/i));
    expect(onSeasonToggle).toHaveBeenCalledWith(2);
  });

  it('marks the active season chip via aria-pressed', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={new Set([2])}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
      />,
    );
    expect(screen.getByLabelText(/Season 2: 8 missing/i))
      .toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByLabelText(/Season 3: 0 missing/i))
      .toHaveAttribute('aria-pressed', 'false');
  });

  it('renders an inline drill panel directly below the active season chip', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={new Set([2])}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        renderDrill={(n) => <span>drill-placeholder-{n}</span>}
      />,
    );
    const slot = screen.getByTestId('queue-drill-slot');
    expect(slot).toHaveAttribute('data-season-number', '2');
    expect(slot).toHaveTextContent('drill-placeholder-2');

    // The drill panel must be a descendant of its specific season's
    // list item — not floating at the bottom of the row.
    const seasonItem = slot.closest('[data-testid="queue-row-season"]');
    expect(seasonItem).not.toBeNull();
    expect(seasonItem?.getAttribute('data-season-number')).toBe('2');
  });

  it('renders a drill panel for every open season simultaneously', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={new Set([2, 3])}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        renderDrill={(n) => <span data-testid={`drill-body-${n}`}>drill-{n}</span>}
      />,
    );
    const slots = screen.getAllByTestId('queue-drill-slot');
    expect(slots).toHaveLength(2);
    const seasonNumbers = slots
      .map((el) => el.getAttribute('data-season-number'))
      .sort();
    expect(seasonNumbers).toEqual(['2', '3']);
    expect(screen.getByTestId('drill-body-2')).toBeInTheDocument();
    expect(screen.getByTestId('drill-body-3')).toBeInTheDocument();
  });

  it('only renders the drill panel for the open chip when others are closed', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={new Set([3])}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        renderDrill={(n) => <span>drill-{n}</span>}
      />,
    );
    const slots = screen.getAllByTestId('queue-drill-slot');
    expect(slots).toHaveLength(1);
    expect(slots[0]).toHaveAttribute('data-season-number', '3');
    expect(slots[0]).toHaveTextContent('drill-3');
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
        openSeasons={EMPTY}
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
        openSeasons={EMPTY}
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

  it('hides every drill panel when no season is open', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={EMPTY}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        renderDrill={() => <span>drill-placeholder</span>}
      />,
    );
    expect(screen.queryByTestId('queue-drill-slot')).not.toBeInTheDocument();
  });

  it('fires onScan and disables the button when in-flight', async () => {
    const onScan = vi.fn();
    const { rerender } = renderWithProviders(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={EMPTY}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={onScan}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: /scan severance now/i }));
    expect(onScan).toHaveBeenCalledTimes(1);
    rerender(
      <QueueRow
        row={row}
        instanceName="alpha"
        instanceUiUrl="https://sonarr.example.com"
        openSeasons={EMPTY}
        isInFlight
        onSeasonToggle={vi.fn()}
        onScan={onScan}
      />,
    );
    expect(screen.getByRole('button', { name: /scan severance now/i })).toBeDisabled();
  });
});
