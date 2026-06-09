import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { TooltipProvider } from '@/components/ui/tooltip';
import { QueueEpisodeChips } from './QueueEpisodeChips';
import type { SeasonEpisodeItem } from '@/lib/api/queueSeasonEpisodes';

const items: SeasonEpisodeItem[] = [
  { number: 1, title: 'Pilot', monitored: true, has_file: true, aired: true, air_date_utc: '2024-01-01T00:00:00Z' },
  { number: 2, title: 'The Reveal', monitored: true, has_file: false, aired: true, air_date_utc: '2024-01-08T00:00:00Z' },
  { number: 3, monitored: true, has_file: false, aired: false, air_date_utc: '2099-01-01T00:00:00Z' },
  { number: 4, monitored: false, has_file: false, aired: true, air_date_utc: '2024-01-15T00:00:00Z' },
];

function withTooltip(ui: React.ReactElement) {
  return <TooltipProvider delayDuration={0}>{ui}</TooltipProvider>;
}

describe('<QueueEpisodeChips />', () => {
  it('renders one chip per episode with correct data-state', () => {
    renderWithProviders(withTooltip(<QueueEpisodeChips items={items} />));
    expect(screen.getByText('E1').getAttribute('data-state')).toBe('have');
    expect(screen.getByText('E2').getAttribute('data-state')).toBe('miss');
    expect(screen.getByText('E3').getAttribute('data-state')).toBe('upcoming');
    expect(screen.getByText('E4').getAttribute('data-state')).toBe('upcoming');
  });

  it('renders nothing when items=[]', () => {
    renderWithProviders(withTooltip(<QueueEpisodeChips items={[]} />));
    const list = screen.getByTestId('queue-episode-chips');
    expect(list.children.length).toBe(0);
  });

  it('surfaces the episode title via tooltip on hover', async () => {
    const user = userEvent.setup();
    renderWithProviders(withTooltip(<QueueEpisodeChips items={items} />));
    await user.hover(screen.getByText('E1'));
    const tip = await screen.findAllByText(/Episode 1: Pilot/i);
    expect(tip.length).toBeGreaterThan(0);
  });

  it('falls back to plain tooltip when title is omitted', async () => {
    const user = userEvent.setup();
    renderWithProviders(withTooltip(<QueueEpisodeChips items={items} />));
    await user.hover(screen.getByText('E3'));
    const tip = await screen.findAllByText(/^Episode 3$/i);
    expect(tip.length).toBeGreaterThan(0);
  });
});
