import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { SeasonsAccordion, resolveSeasonLabel } from './SeasonsAccordion';

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
    r(<SeasonsAccordion seriesId={42} seasons={seasons} />);
    const items = screen.getAllByTestId('season-accordion-item');
    expect(items).toHaveLength(4);
    expect(items[0]!.getAttribute('data-season')).toBe('3');
    expect(items[1]!.getAttribute('data-season')).toBe('2');
    expect(items[2]!.getAttribute('data-season')).toBe('1');
    expect(items[3]!.getAttribute('data-season')).toBe('0');
    expect(items[3]!.getAttribute('data-special')).toBe('true');
  });

  it('expands and renders episodes (lazy fetch overrides composite payload)', () => {
    r(<SeasonsAccordion seriesId={42} seasons={seasons} />);
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
      r(<SeasonsAccordion seriesId={42} seasons={seasons} />);
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
    r(<SeasonsAccordion seriesId={42} seasons={[]} />);
    expect(screen.getByText(/No seasons available yet/)).toBeInTheDocument();
  });

  // Story 970 / C3c-2: per-season downloading chip now sourced from the
  // /library counts (librarySeasons), NOT season.downloading_count.
  it('renders the downloading chip when librarySeasons has downloading > 0', () => {
    const fixture = [{
      season_number: 5, episode_count: 10, monitored: true,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    const lib = new Map([[5, { onDisk: 7, downloading: 2 }]]);
    r(<SeasonsAccordion seriesId={42} seasons={fixture} librarySeasons={lib} />);
    const chip = screen.getByTestId('season-downloading-chip');
    expect(chip.getAttribute('data-season')).toBe('5');
    expect(chip.textContent).toMatch(/2/);
  });

  it('omits the downloading chip when librarySeasons downloading is 0', () => {
    const fixture = [{
      season_number: 5, episode_count: 10, monitored: true,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    const lib = new Map([[5, { onDisk: 7, downloading: 0 }]]);
    r(<SeasonsAccordion seriesId={42} seasons={fixture} librarySeasons={lib} />);
    expect(screen.queryByTestId('season-downloading-chip')).not.toBeInTheDocument();
  });

  // Story 970 / C3c-2: on-disk "X/total" renders at the LIST level (no expand)
  // when librarySeasons carries an entry for the season.
  it('renders "X/total on disk" at the list level from librarySeasons', () => {
    const fixture = [{
      season_number: 5, episode_count: 10, monitored: true,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    const lib = new Map([[5, { onDisk: 6, downloading: 0 }]]);
    r(<SeasonsAccordion seriesId={42} seasons={fixture} librarySeasons={lib} />);
    const onDisk = screen.getByTestId('season-on-disk');
    expect(onDisk.getAttribute('data-season')).toBe('5');
    expect(onDisk.textContent).toMatch(/6/);
    expect(onDisk.textContent).toMatch(/10/);
  });

  // Story 970 / C3c-2: no library entry (TMDB-only / cold) ⇒ totals only,
  // NO misleading "0/total" on-disk line, no chip, no crash.
  it('shows totals only (no on-disk line) when librarySeasons is absent', () => {
    const fixture = [{
      season_number: 5, episode_count: 10, monitored: true,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    r(<SeasonsAccordion seriesId={42} seasons={fixture} />);
    expect(screen.queryByTestId('season-on-disk')).not.toBeInTheDocument();
    expect(screen.queryByTestId('season-downloading-chip')).not.toBeInTheDocument();
    expect(screen.getByTestId('season-accordion-item')).toBeInTheDocument();
  });

  it('shows totals only for seasons missing from a partial librarySeasons map', () => {
    const fixture = [
      {
        season_number: 5, episode_count: 10, monitored: true,
        episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
      },
      {
        season_number: 6, episode_count: 8, monitored: true,
        episodes: [{ episode_number: 1, title: 'B', has_file: false, monitored: true }],
      },
    ];
    const lib = new Map([[5, { onDisk: 3, downloading: 0 }]]);
    r(<SeasonsAccordion seriesId={42} seasons={fixture} librarySeasons={lib} />);
    const onDiskEls = screen.getAllByTestId('season-on-disk');
    expect(onDiskEls).toHaveLength(1);
    expect(onDiskEls[0]!.getAttribute('data-season')).toBe('5');
  });

  // Bug 973: the localized numbered label wins over a RU-leaked season.name.
  it('renders the localized numbered label, not the RU-leaked season.name', () => {
    const fixture = [{
      season_number: 4, name: 'Сезон 4', episode_count: 8, monitored: true,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    r(<SeasonsAccordion seriesId={42} seasons={fixture} />);
    const item = screen.getByTestId('season-accordion-item');
    expect(item.textContent).toContain('Season 4');
    expect(item.textContent).not.toContain('Сезон 4');
  });

  it('renders a genuine custom season title verbatim', () => {
    const fixture = [{
      season_number: 1, name: 'Book One: Water', episode_count: 20, monitored: true,
      episodes: [{ episode_number: 1, title: 'A', has_file: false, monitored: true }],
    }];
    r(<SeasonsAccordion seriesId={42} seasons={fixture} />);
    expect(screen.getByText('Book One: Water')).toBeInTheDocument();
  });
});

describe('resolveSeasonLabel (bug 973)', () => {
  const tEn = i18n.getFixedT('en-US');
  const tRu = i18n.getFixedT('ru-RU');

  it('normalises a RU-leaked numbered name to the localized label under en', () => {
    expect(resolveSeasonLabel({ season_number: 4, name: 'Сезон 4' }, tEn)).toBe('Season 4');
  });

  it('normalises a plain English numbered name too', () => {
    expect(resolveSeasonLabel({ season_number: 4, name: 'Season 4' }, tEn)).toBe('Season 4');
  });

  it('renders "Сезон {n}" under the ru UI locale for a numbered season', () => {
    expect(resolveSeasonLabel({ season_number: 4, name: 'Season 4' }, tRu)).toBe('Сезон 4');
  });

  it('preserves a genuine custom title verbatim', () => {
    expect(resolveSeasonLabel({ season_number: 1, name: 'Book One: Water' }, tEn)).toBe('Book One: Water');
  });

  it('falls back to the numbered label for an empty name', () => {
    expect(resolveSeasonLabel({ season_number: 2, name: '' }, tEn)).toBe('Season 2');
    expect(resolveSeasonLabel({ season_number: 2 }, tEn)).toBe('Season 2');
  });

  it('renders the Specials label for season 0, ignoring any name', () => {
    expect(resolveSeasonLabel({ season_number: 0, name: 'Спецвыпуски' }, tEn)).toBe('Specials');
    expect(resolveSeasonLabel({ season_number: 0, name: 'Whatever' }, tEn)).toBe('Specials');
  });

  it('normalises a localized specials name on a non-zero-guarded path', () => {
    // e.g. if ever a specials-worded name arrives on season 0 it is still Specials
    expect(resolveSeasonLabel({ season_number: 0, name: 'Especiales' }, tEn)).toBe('Specials');
  });
});
