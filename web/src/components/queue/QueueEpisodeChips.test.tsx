import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { QueueEpisodeChips } from './QueueEpisodeChips';
import type { SeasonEpisodeItem } from '@/lib/api/queueSeasonEpisodes';

const items: SeasonEpisodeItem[] = [
  { number: 1, monitored: true, has_file: true, aired: true, air_date_utc: '2024-01-01T00:00:00Z' },
  { number: 2, monitored: true, has_file: false, aired: true, air_date_utc: '2024-01-08T00:00:00Z' },
  { number: 3, monitored: true, has_file: false, aired: false, air_date_utc: '2099-01-01T00:00:00Z' },
  { number: 4, monitored: false, has_file: false, aired: true, air_date_utc: '2024-01-15T00:00:00Z' },
];

describe('<QueueEpisodeChips />', () => {
  it('renders one chip per episode with correct data-state', () => {
    renderWithProviders(<QueueEpisodeChips items={items} />);
    expect(screen.getByText('E1').getAttribute('data-state')).toBe('have');
    expect(screen.getByText('E2').getAttribute('data-state')).toBe('miss');
    expect(screen.getByText('E3').getAttribute('data-state')).toBe('upcoming');
    expect(screen.getByText('E4').getAttribute('data-state')).toBe('upcoming');
  });

  it('renders nothing when items=[]', () => {
    renderWithProviders(<QueueEpisodeChips items={[]} />);
    const list = screen.getByTestId('queue-episode-chips');
    expect(list.children.length).toBe(0);
  });
});
