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
    season_number: 3, episode_count: 1, air_date: '2026-01-12',
    on_disk_count: 0, monitored: true, poster_asset: 'pa',
    episodes: [{ episode_number: 1, title: 'S3E1', has_file: false, monitored: true }],
  },
  {
    season_number: 2, episode_count: 1, air_date: '2025-01-12',
    on_disk_count: 0, monitored: true, poster_asset: 'pa',
    episodes: [{ episode_number: 1, title: 'S2E1', has_file: false, monitored: true }],
  },
  {
    season_number: 0, episode_count: 1, on_disk_count: 0, monitored: false,
    episodes: [{ episode_number: 1, title: 'Special', has_file: false, monitored: false }],
  },
];

describe('<SeasonsAccordion />', () => {
  it('renders seasons DESC with Specials pinned to the end', () => {
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={seasons} />);
    const items = screen.getAllByTestId('season-accordion-item');
    expect(items).toHaveLength(4);
    expect(items[0]!.getAttribute('data-season')).toBe('3');
    expect(items[1]!.getAttribute('data-season')).toBe('2');
    expect(items[2]!.getAttribute('data-season')).toBe('1');
    expect(items[3]!.getAttribute('data-season')).toBe('0');
    expect(items[3]!.getAttribute('data-special')).toBe('true');
  });

  it('expands and renders episodes (lazy fetch overrides composite payload)', () => {
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={seasons} />);
    fireEvent.click(screen.getAllByRole('button')[0]!);
    expect(screen.getByText('Lazy')).toBeInTheDocument();
  });

  it('renders episodes in DESC order (highest episode_number first)', async () => {
    const { useSeriesSeason } = await import('@/api/seriesSeason');
    const mocked = vi.mocked(useSeriesSeason);
    mocked.mockImplementation(({ enabled }: { enabled?: boolean }) => ({
      data: enabled ? { season: { episodes: [
        { episode_number: 1, title: 'EpOne',   has_file: false, monitored: true },
        { episode_number: 2, title: 'EpTwo',   has_file: false, monitored: true },
        { episode_number: 3, title: 'EpThree', has_file: false, monitored: true },
      ] } } : undefined,
      isPending: false,
      isError: false,
    }) as unknown as ReturnType<typeof useSeriesSeason>);
    try {
      r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={seasons} />);
      fireEvent.click(screen.getAllByRole('button')[0]!);
      const rows = screen.getAllByTestId('episode-row');
      expect(rows).toHaveLength(3);
      expect(rows[0]!.textContent).toContain('EpThree');
      expect(rows[1]!.textContent).toContain('EpTwo');
      expect(rows[2]!.textContent).toContain('EpOne');
    } finally {
      mocked.mockReset();
      mocked.mockImplementation(({ enabled }: { enabled?: boolean }) => ({
        data: enabled ? { season: { episodes: [{ episode_number: 1, title: 'Lazy', has_file: false, monitored: true }] } } : undefined,
        isPending: false,
        isError: false,
      }) as unknown as ReturnType<typeof useSeriesSeason>);
    }
  });

  it('renders the empty-state line when seasons is empty', () => {
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={[]} />);
    expect(screen.getByText(/No seasons available yet/)).toBeInTheDocument();
  });

  // Story 379: per-season downloading chip.
  it('renders the downloading chip when season.downloading_count > 0', () => {
    const fixture = [{
      season_number: 5, episode_count: 10, on_disk_count: 7, monitored: true,
      downloading_count: 2,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={fixture} />);
    const chip = screen.getByTestId('season-downloading-chip');
    expect(chip.getAttribute('data-season')).toBe('5');
    expect(chip.textContent).toMatch(/2/);
  });

  it('omits the downloading chip when downloading_count is 0', () => {
    const fixture = [{
      season_number: 5, episode_count: 10, on_disk_count: 7, monitored: true,
      downloading_count: 0,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    r(<SeasonsAccordion instance="alpha" seriesId={42} seasons={fixture} />);
    expect(screen.queryByTestId('season-downloading-chip')).not.toBeInTheDocument();
  });
});
