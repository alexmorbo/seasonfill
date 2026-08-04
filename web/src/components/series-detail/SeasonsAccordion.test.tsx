import { describe, it, expect, vi, beforeEach } from 'vitest';
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

// ADR-0012 S2 — controllable useMonitorSeason. `mockMutate` records the vars +
// options passed by the row; `mockPending.value` drives the button disabled
// state. Both are `mock`-prefixed so vitest's vi.mock hoisting permits the
// factory to close over them.
const mockMutate = vi.fn();
const mockPending = { value: false };
vi.mock('@/api/seasonMonitor', () => ({
  useMonitorSeason: () => ({ mutate: mockMutate, isPending: mockPending.value }),
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

// ADR-0012 S2 — per-season monitor/request affordance.
describe('<SeasonsAccordion /> — season request/monitor affordance', () => {
  const oneSeason = [{
    season_number: 1, episode_count: 2, air_date: '2024-01-12', monitored: true,
    episodes: [{ episode_number: 1, title: 'Pilot', has_file: true, monitored: true }],
  }];

  beforeEach(() => {
    mockMutate.mockReset();
    mockPending.value = false;
  });

  it('renders the request button (no badge) for an unmonitored season under a selected instance', () => {
    r(<SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="main" />);
    expect(screen.getByTestId('season-request-button')).toBeInTheDocument();
    expect(screen.queryByTestId('season-monitored-badge')).not.toBeInTheDocument();
  });

  it('renders the monitored badge (no button) when librarySeasons reports monitored', () => {
    const lib = new Map([[1, { onDisk: 2, downloading: 0, monitored: true }]]);
    r(<SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="main" librarySeasons={lib} />);
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    expect(screen.queryByTestId('season-request-button')).not.toBeInTheDocument();
  });

  it('renders no season-action when there is no selected instance', () => {
    r(<SeasonsAccordion seriesId={42} seasons={oneSeason} />);
    expect(screen.queryByTestId('season-action')).not.toBeInTheDocument();
    expect(screen.queryByTestId('season-request-button')).not.toBeInTheDocument();
    expect(screen.queryByTestId('season-monitored-badge')).not.toBeInTheDocument();
  });

  it('calls mutate with { instance, seriesId, seasonNumber } and an onSuccess option on click', () => {
    r(<SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="main" />);
    fireEvent.click(screen.getByTestId('season-request-button'));
    expect(mockMutate).toHaveBeenCalledTimes(1);
    const [vars, opts] = mockMutate.mock.calls[0]!;
    expect(vars).toEqual({ instance: 'main', seriesId: 42, seasonNumber: 1 });
    expect(typeof (opts as { onSuccess?: unknown }).onSuccess).toBe('function');
  });

  it('optimistically flips to the monitored badge when the mutation onSuccess fires', () => {
    // Drive onSuccess synchronously so the row's justRequested state flips.
    mockMutate.mockImplementation((_vars, opts) => opts?.onSuccess?.());
    r(<SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="main" />);
    fireEvent.click(screen.getByTestId('season-request-button'));
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    expect(screen.queryByTestId('season-request-button')).not.toBeInTheDocument();
  });

  it('resets the optimistic state when the selected instance changes', () => {
    mockMutate.mockImplementation((_vars, opts) => opts?.onSuccess?.());
    const { rerender } = r(
      <SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="main" />,
    );
    fireEvent.click(screen.getByTestId('season-request-button'));
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    // Switching scope re-anchors the row: the optimistic flag clears and the
    // (still-unmonitored) request button reappears for the new instance.
    rerender(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={new QueryClient()}>
          <SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="alt" />
        </QueryClientProvider>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('season-request-button')).toBeInTheDocument();
    expect(screen.queryByTestId('season-monitored-badge')).not.toBeInTheDocument();
  });

  it('renders the instanceSelector slot inside the section heading', () => {
    r(
      <SeasonsAccordion
        seriesId={42}
        seasons={oneSeason}
        selectedInstance="main"
        instanceSelector={<span data-testid="sel-slot">SEL</span>}
      />,
    );
    const heading = document.getElementById('seasons-accordion-heading');
    expect(heading).not.toBeNull();
    expect(heading!.querySelector('[data-testid="sel-slot"]')).not.toBeNull();
  });

  it('keeps the accordion content collapsed after clicking request (sibling, not descendant)', () => {
    // jsdom cannot exercise the real Radix pointer-toggle path; the structural
    // guarantee here is that the action is a SIBLING of the trigger button, so
    // clicking it never expands the row. Real no-toggle behaviour is asserted
    // in Playwright.
    r(<SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="main" />);
    fireEvent.click(screen.getByTestId('season-request-button'));
    // Lazy episode content ('Lazy' from the seriesSeason mock) is only rendered
    // when the row expands — it must stay absent.
    expect(screen.queryByText('Lazy')).not.toBeInTheDocument();
  });

  it('disables the request button while a request is pending', () => {
    mockPending.value = true;
    r(<SeasonsAccordion seriesId={42} seasons={oneSeason} selectedInstance="main" />);
    expect(screen.getByTestId('season-request-button')).toBeDisabled();
  });
});
