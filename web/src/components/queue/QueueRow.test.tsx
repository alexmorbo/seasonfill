import { describe, it, expect, vi } from 'vitest';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { QueueRow } from './QueueRow';

const row = {
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
  it('renders the title, year, missing pill, and season chips', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
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
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
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
        instanceUiUrl="https://sonarr.example.com"
        openSeason={2}
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

  it('renders the drill slot when a season is open', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceUiUrl="https://sonarr.example.com"
        openSeason={2}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        drillSlot={<span>drill-placeholder</span>}
      />,
    );
    const slot = screen.getByTestId('queue-drill-slot');
    expect(slot).toHaveAttribute('data-season-number', '2');
    expect(slot).toHaveTextContent('drill-placeholder');
  });

  it('hides the drill slot when no season is open', () => {
    renderWithProviders(
      <QueueRow
        row={row}
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight={false}
        onSeasonToggle={vi.fn()}
        onScan={vi.fn()}
        drillSlot={<span>drill-placeholder</span>}
      />,
    );
    expect(screen.queryByTestId('queue-drill-slot')).not.toBeInTheDocument();
  });

  it('fires onScan and disables the button when in-flight', async () => {
    const onScan = vi.fn();
    const { rerender } = renderWithProviders(
      <QueueRow
        row={row}
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
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
        instanceUiUrl="https://sonarr.example.com"
        openSeason={null}
        isInFlight
        onSeasonToggle={vi.fn()}
        onScan={onScan}
      />,
    );
    expect(screen.getByRole('button', { name: /scan severance now/i })).toBeDisabled();
  });
});
