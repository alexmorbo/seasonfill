import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

// ADR-0012 S3 — controllable useMonitorSeason. `mockMonitorMutate` records the
// vars + options passed by the present-in-target path; `mockMonitorPending.value`
// drives the button disabled state. All holders are `mock`-prefixed so vitest's
// vi.mock hoisting permits the factories to close over them.
const mockMonitorMutate = vi.fn();
const mockMonitorPending = { value: false };
vi.mock('@/api/seasonMonitor', () => ({
  useMonitorSeason: () => ({ mutate: mockMonitorMutate, isPending: mockMonitorPending.value }),
}));

// ADR-0012 S3 — the absent-in-target one-click add path.
const mockAddMutate = vi.fn();
const mockAddPending = { value: false };
vi.mock('@/api/discovery', () => ({
  useAddToSonarr: () => ({ mutate: mockAddMutate, isPending: mockAddPending.value }),
}));

// ADR-0012 S3 — the fallback path opens the add modal preset to the instance.
const mockOpenAddToSonarr = vi.fn();
vi.mock('@/components/discovery/add-to-sonarr-context', () => ({
  useAddToSonarrLauncher: () => ({ openAddToSonarr: mockOpenAddToSonarr, target: null, close: vi.fn() }),
}));

// ADR-0012 S3 — configured instances (with optional ADR-0009 defaults).
const mockInstances = {
  value: [] as Array<{ name: string; default_quality_profile_id?: number; default_root_folder_path?: string }>,
};
vi.mock('@/lib/instances', () => ({
  useInstances: () => ({ data: { instances: mockInstances.value }, isPending: false }),
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

// ADR-0012 S3 — per-season split-button request affordance.
describe('<SeasonsAccordion /> — season request split-button', () => {
  // seasonNumber 2 throughout; the primary button targets defaultInstance="main".
  const oneSeason = [{
    season_number: 2, episode_count: 2, air_date: '2025-01-12', monitored: true,
    episodes: [{ episode_number: 1, title: 'Pilot', has_file: true, monitored: true }],
  }];

  function node(props: Record<string, unknown> = {}) {
    return (
      <SeasonsAccordion
        seriesId={42}
        seasons={oneSeason}
        defaultInstance="main"
        inLibraryInstances={['main']}
        title="Ted"
        tvdbId={99}
        {...props}
      />
    );
  }
  const renderAccordion = (props: Record<string, unknown> = {}) => r(node(props));

  beforeEach(() => {
    mockMonitorMutate.mockReset();
    mockAddMutate.mockReset();
    mockOpenAddToSonarr.mockReset();
    mockMonitorPending.value = false;
    mockAddPending.value = false;
    mockInstances.value = [{ name: 'main' }];
  });

  it('1. single instance unmonitored → request button, no badge, no caret', () => {
    mockInstances.value = [{ name: 'main' }];
    renderAccordion();
    expect(screen.getByTestId('season-request-button')).toBeInTheDocument();
    expect(screen.queryByTestId('season-monitored-badge')).not.toBeInTheDocument();
    expect(screen.queryByTestId('season-action-caret')).not.toBeInTheDocument();
  });

  it('2. single instance + monitored library → badge, no button, no caret', () => {
    mockInstances.value = [{ name: 'main' }];
    const lib = new Map([[2, { onDisk: 2, downloading: 0, monitored: true }]]);
    renderAccordion({ librarySeasons: lib });
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    expect(screen.queryByTestId('season-request-button')).not.toBeInTheDocument();
    expect(screen.queryByTestId('season-action-caret')).not.toBeInTheDocument();
  });

  it('3. no defaultInstance → no season-action', () => {
    mockInstances.value = [{ name: 'main' }];
    renderAccordion({ defaultInstance: undefined });
    expect(screen.queryByTestId('season-action')).not.toBeInTheDocument();
  });

  it('4. >1 instances → request button + caret present', () => {
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    renderAccordion();
    expect(screen.getByTestId('season-request-button')).toBeInTheDocument();
    expect(screen.getByTestId('season-action-caret')).toBeInTheDocument();
  });

  it('5. caret menu lists only non-default instances (default excluded)', async () => {
    const user = userEvent.setup();
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    renderAccordion();
    await user.click(screen.getByTestId('season-action-caret'));
    const other = await screen.findByTestId('season-menu-instance-other');
    expect(other.textContent).toContain('Request in other');
    expect(screen.queryByTestId('season-menu-instance-main')).not.toBeInTheDocument();
  });

  it('6. monitored in default + >1 instances → badge AND caret', () => {
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    const lib = new Map([[2, { onDisk: 2, downloading: 0, monitored: true }]]);
    renderAccordion({ librarySeasons: lib });
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    expect(screen.getByTestId('season-action-caret')).toBeInTheDocument();
  });

  it('7. present-in-target primary click → monitor mutate, no add', () => {
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    renderAccordion({ inLibraryInstances: ['main', 'other'] });
    fireEvent.click(screen.getByTestId('season-request-button'));
    expect(mockMonitorMutate).toHaveBeenCalledTimes(1);
    const [vars, opts] = mockMonitorMutate.mock.calls[0]!;
    expect(vars).toEqual({ instance: 'main', seriesId: 42, seasonNumber: 2 });
    expect(typeof (opts as { onSuccess?: unknown }).onSuccess).toBe('function');
    expect(mockAddMutate).not.toHaveBeenCalled();
  });

  it('8. absent-in-target WITH defaults → one-click add with ADR-0009 defaults', async () => {
    const user = userEvent.setup();
    mockInstances.value = [
      { name: 'main' },
      { name: 'other', default_quality_profile_id: 3, default_root_folder_path: '/tv' },
    ];
    renderAccordion({ inLibraryInstances: ['main'] });
    await user.click(screen.getByTestId('season-action-caret'));
    await user.click(await screen.findByTestId('season-menu-instance-other'));
    expect(mockAddMutate).toHaveBeenCalledTimes(1);
    const [body] = mockAddMutate.mock.calls[0]!;
    expect(body).toMatchObject({
      instance_name: 'other',
      tvdb_id: 99,
      quality_profile_id: 3,
      root_folder_path: '/tv',
      monitored_seasons: [2],
      search_on_add: true,
    });
    expect(mockOpenAddToSonarr).not.toHaveBeenCalled();
  });

  it('9. absent-in-target defaults MISSING → open add modal, no add mutate', async () => {
    const user = userEvent.setup();
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    renderAccordion({ inLibraryInstances: ['main'] });
    await user.click(screen.getByTestId('season-action-caret'));
    await user.click(await screen.findByTestId('season-menu-instance-other'));
    expect(mockOpenAddToSonarr).toHaveBeenCalledWith(
      expect.objectContaining({ instanceName: 'other' }),
    );
    expect(mockAddMutate).not.toHaveBeenCalled();
  });

  it('10. absent-in-target tvdb MISSING → open add modal, no add mutate', async () => {
    const user = userEvent.setup();
    mockInstances.value = [
      { name: 'main' },
      { name: 'other', default_quality_profile_id: 3, default_root_folder_path: '/tv' },
    ];
    renderAccordion({ inLibraryInstances: ['main'], tvdbId: undefined });
    await user.click(screen.getByTestId('season-action-caret'));
    await user.click(await screen.findByTestId('season-menu-instance-other'));
    expect(mockOpenAddToSonarr).toHaveBeenCalled();
    expect(mockAddMutate).not.toHaveBeenCalled();
  });

  it('11. optimistic flip: default-instance monitor onSuccess shows the badge', () => {
    mockInstances.value = [{ name: 'main' }];
    mockMonitorMutate.mockImplementation((_v, opts) => opts?.onSuccess?.());
    renderAccordion();
    fireEvent.click(screen.getByTestId('season-request-button'));
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    expect(screen.queryByTestId('season-request-button')).not.toBeInTheDocument();
  });

  it('12. caret request into a NON-default instance does NOT flip the badge', async () => {
    const user = userEvent.setup();
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    mockMonitorMutate.mockImplementation((_v, opts) => opts?.onSuccess?.());
    renderAccordion({ inLibraryInstances: ['main', 'other'] });
    await user.click(screen.getByTestId('season-action-caret'));
    await user.click(await screen.findByTestId('season-menu-instance-other'));
    // targetName 'other' !== defaultInstance 'main' ⇒ no optimistic flip.
    expect(screen.getByTestId('season-request-button')).toBeInTheDocument();
  });

  it('13. resets optimistic state on defaultInstance change', () => {
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    mockMonitorMutate.mockImplementation((_v, opts) => opts?.onSuccess?.());
    const { rerender } = renderAccordion({ inLibraryInstances: ['main', 'other'] });
    fireEvent.click(screen.getByTestId('season-request-button'));
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    rerender(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={new QueryClient()}>
          {node({ inLibraryInstances: ['main', 'other'], defaultInstance: 'other' })}
        </QueryClientProvider>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('season-request-button')).toBeInTheDocument();
    expect(screen.queryByTestId('season-monitored-badge')).not.toBeInTheDocument();
  });

  it('14. disables the primary button and caret while a request is pending', () => {
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    mockMonitorPending.value = true;
    renderAccordion();
    expect(screen.getByTestId('season-request-button')).toBeDisabled();
    expect(screen.getByTestId('season-action-caret')).toBeDisabled();
  });

  it('15. keeps content collapsed after request; action is a sibling of the trigger', () => {
    // jsdom cannot verify the real Radix pointer-propagation path — live browser
    // verification is required. The structural guarantee asserted here is that the
    // action is NOT a descendant of the trigger button, so clicking it can never
    // expand the row.
    mockInstances.value = [{ name: 'main' }];
    renderAccordion();
    fireEvent.click(screen.getByTestId('season-request-button'));
    // Lazy episode content ('Lazy' from the seriesSeason mock) only renders on
    // expand — it must stay absent.
    expect(screen.queryByText('Lazy')).not.toBeInTheDocument();
    expect(
      screen.getByTestId('season-action').closest('button[aria-expanded]'),
    ).toBeNull();
  });

  // ADR-0012 S5 — the caret offers only instances that LACK the season.
  it('16. season monitored in BOTH instances → badge, NO caret', () => {
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    const lib = new Map([[2, { onDisk: 2, downloading: 0, monitored: true }]]);
    const mbi = new Map<string, ReadonlySet<number>>([
      ['main', new Set([2])],
      ['other', new Set([2])],
    ]);
    renderAccordion({
      librarySeasons: lib,
      inLibraryInstances: ['main', 'other'],
      monitoredByInstance: mbi,
    });
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    expect(screen.queryByTestId('season-action-caret')).not.toBeInTheDocument();
  });

  it('17. season monitored in only the default → caret lists ONLY the other', async () => {
    const user = userEvent.setup();
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    const lib = new Map([[2, { onDisk: 2, downloading: 0, monitored: true }]]);
    const mbi = new Map<string, ReadonlySet<number>>([
      ['main', new Set([2])],
      ['other', new Set<number>()],
    ]);
    renderAccordion({
      librarySeasons: lib,
      inLibraryInstances: ['main', 'other'],
      monitoredByInstance: mbi,
    });
    expect(screen.getByTestId('season-monitored-badge')).toBeInTheDocument();
    await user.click(screen.getByTestId('season-action-caret'));
    expect(await screen.findByTestId('season-menu-instance-other')).toBeInTheDocument();
    expect(screen.queryByTestId('season-menu-instance-main')).not.toBeInTheDocument();
  });

  it('18. season present nowhere (S1) → request button + caret listing the other', async () => {
    const user = userEvent.setup();
    mockInstances.value = [{ name: 'main' }, { name: 'other' }];
    renderAccordion({ inLibraryInstances: [] });
    expect(screen.getByTestId('season-request-button')).toBeInTheDocument();
    await user.click(screen.getByTestId('season-action-caret'));
    expect(await screen.findByTestId('season-menu-instance-other')).toBeInTheDocument();
    expect(screen.queryByTestId('season-menu-instance-main')).not.toBeInTheDocument();
  });
});
