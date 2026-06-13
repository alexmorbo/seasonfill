import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { NextEpisodeCard } from './NextEpisodeCard';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<NextEpisodeCard />', () => {
  it('renders the "next" variant with code and title', () => {
    r(<NextEpisodeCard
      nextEpisode={{ season_number: 5, episode_number: 3, title: 'Glasnost', air_date: '2026-07-14' }}
      status="continuing"
    />);
    expect(screen.getByTestId('next-episode-card').getAttribute('data-variant')).toBe('next');
    expect(screen.getByText(/S05E03/)).toBeInTheDocument();
    expect(screen.getByText(/Glasnost/)).toBeInTheDocument();
  });

  it('renders the "ended" variant when status=ended', () => {
    r(<NextEpisodeCard status="ended" yearEnd={2024} />);
    expect(screen.getByTestId('next-episode-card').getAttribute('data-variant')).toBe('ended');
  });

  it('renders the "production" variant when status=in_production', () => {
    r(<NextEpisodeCard status="in_production" />);
    expect(screen.getByTestId('next-episode-card').getAttribute('data-variant')).toBe('production');
  });

  it('renders the "unscheduled" variant when continuing with no nextEpisode', () => {
    r(<NextEpisodeCard status="continuing" />);
    expect(screen.getByTestId('next-episode-card').getAttribute('data-variant')).toBe('unscheduled');
  });
});
