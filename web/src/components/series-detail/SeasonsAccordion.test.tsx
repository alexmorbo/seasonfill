import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { SeasonsAccordion } from './SeasonsAccordion';

vi.mock('@/api/seriesSeason', () => ({
  useSeriesSeason: vi.fn(({ enabled }) => ({
    data: enabled ? { season: { episodes: [{ episode_number: 1, title: 'Lazy', has_file: false, monitored: true }] } } : undefined,
    isPending: false,
    isError: false,
  })),
}));

function r(node: React.ReactElement) {
  const qc = new QueryClient();
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>{node}</QueryClientProvider>
    </I18nextProvider>,
  );
}

const seasons = [
  {
    season_number: 1, episode_count: 2, air_date: '2024-01-12',
    on_disk_count: 1, monitored: true, poster_asset: 'pa',
    episodes: [
      { episode_number: 1, title: 'Pilot', has_file: true, monitored: true },
      { episode_number: 2, title: 'Two',   has_file: false, monitored: true },
    ],
  },
  {
    season_number: 0, episode_count: 1, on_disk_count: 0, monitored: false,
    episodes: [{ episode_number: 1, title: 'Special', has_file: false, monitored: false }],
  },
];

describe('<SeasonsAccordion />', () => {
  it('renders one accordion item per season, Specials pushed to the end', () => {
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={seasons} />);
    const items = screen.getAllByTestId('season-accordion-item');
    expect(items).toHaveLength(2);
    expect(items[0]!.getAttribute('data-season')).toBe('1');
    expect(items[1]!.getAttribute('data-season')).toBe('0');
    expect(items[1]!.getAttribute('data-special')).toBe('true');
  });

  it('expands and renders episodes (lazy fetch overrides composite payload)', () => {
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={seasons} />);
    fireEvent.click(screen.getAllByRole('button')[0]!);
    expect(screen.getByText('Lazy')).toBeInTheDocument();
  });

  it('renders the empty-state line when seasons is empty', () => {
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={[]} />);
    expect(screen.getByText(/No seasons available yet/)).toBeInTheDocument();
  });
});
