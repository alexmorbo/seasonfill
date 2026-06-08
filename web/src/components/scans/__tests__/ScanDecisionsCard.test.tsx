import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { ScanDecisionsCard } from '../ScanDecisionsCard';
import type { SeriesGroup as SeriesGroupModel } from '@/lib/decision-grouping';
import { createRef } from 'react';

const qc = new QueryClient();
const wrap = (n: React.ReactElement) => (
  <I18nextProvider i18n={i18n}>
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <TooltipProvider>{n}</TooltipProvider>
      </MemoryRouter>
    </QueryClientProvider>
  </I18nextProvider>
);

const baseGroup: SeriesGroupModel = {
  seriesId: 1, seriesTitle: 'Severance', firstSeenIndex: 0,
  worstCategory: 'action_taken',
  seasons: [{ seasonNumber: 1, decision: {
    id: 'd1', scan_run_id: 'abc', series_id: 1, series_title: 'Severance',
    season_number: 1, decision: 'grab', category: 'action_taken', reason: 'grab_selected',
  } }],
} as SeriesGroupModel;

const mkGroup = (
  seriesId: number, seriesTitle: string, worstCategory: SeriesGroupModel['worstCategory'],
  firstSeenIndex: number,
): SeriesGroupModel => ({
  seriesId, seriesTitle, firstSeenIndex, worstCategory,
  seasons: [{ seasonNumber: 1, decision: {
    id: `d-${seriesId}`, scan_run_id: 'abc', series_id: seriesId, series_title: seriesTitle,
    season_number: 1, decision: 'skip', category: worstCategory, reason: 'skip_no_missing',
  } }],
} as SeriesGroupModel);

describe('<ScanDecisionsCard />', () => {
  it('renders the result filter dropdown', () => {
    const ref = createRef<HTMLDivElement>();
    render(wrap(<ScanDecisionsCard
      groups={[baseGroup]} totalSeasons={1} outcome="all"
      expanded={new Set(['Severance'])}
      isPending={false} isFetchingNext={false}
      sentinelRef={ref as React.RefObject<HTMLDivElement>}
      onOutcomeChange={vi.fn()} onToggleSeries={vi.fn()} onOpenDecision={vi.fn()}
    />));
    expect(screen.getByTestId('scan-result-filter')).toBeInTheDocument();
    expect(screen.getByTestId('scan-decisions-card')).toBeInTheDocument();
  });

  it('renders empty state when groups.length === 0 and not pending', () => {
    const ref = createRef<HTMLDivElement>();
    render(wrap(<ScanDecisionsCard
      groups={[]} totalSeasons={0} outcome="all"
      expanded={new Set()}
      isPending={false} isFetchingNext={false}
      sentinelRef={ref as React.RefObject<HTMLDivElement>}
      onOutcomeChange={vi.fn()} onToggleSeries={vi.fn()} onOpenDecision={vi.fn()}
    />));
    expect(screen.getByText(/no decisions for this scan|нет решений/i)).toBeInTheDocument();
  });

  describe('skip-all-complete reveal toggle (F-P1-10)', () => {
    // 2 actionable + 3 all_complete series. Default-hide collapses to 2;
    // toggle reveals all 5; toggle again collapses back to 2.
    const groups: SeriesGroupModel[] = [
      mkGroup(1, 'Severance', 'action_taken', 0),
      mkGroup(2, 'Dark',      'error',        1),
      mkGroup(3, 'Andor',     'all_complete', 2),
      mkGroup(4, 'Loki',      'all_complete', 3),
      mkGroup(5, 'Foundation','all_complete', 4),
    ];

    it('hides all_complete series by default and surfaces a reveal toggle with the hidden count', () => {
      const ref = createRef<HTMLDivElement>();
      render(wrap(<ScanDecisionsCard
        groups={groups} totalSeasons={5} outcome="all"
        expanded={new Set()}
        isPending={false} isFetchingNext={false}
        sentinelRef={ref as React.RefObject<HTMLDivElement>}
        onOutcomeChange={vi.fn()} onToggleSeries={vi.fn()} onOpenDecision={vi.fn()}
      />));
      const titles = screen.getAllByTestId('series-title').map((n) => n.textContent);
      expect(titles).toEqual(['Severance', 'Dark']);
      const toggle = screen.getByTestId('scan-decisions-skip-toggle');
      expect(toggle).toHaveAttribute('aria-pressed', 'false');
      expect(toggle.textContent ?? '').toMatch(/3/);
    });

    it('click reveals hidden series; click again hides them', () => {
      const ref = createRef<HTMLDivElement>();
      render(wrap(<ScanDecisionsCard
        groups={groups} totalSeasons={5} outcome="all"
        expanded={new Set()}
        isPending={false} isFetchingNext={false}
        sentinelRef={ref as React.RefObject<HTMLDivElement>}
        onOutcomeChange={vi.fn()} onToggleSeries={vi.fn()} onOpenDecision={vi.fn()}
      />));
      const toggle = screen.getByTestId('scan-decisions-skip-toggle');
      fireEvent.click(toggle);
      expect(screen.getAllByTestId('series-title')).toHaveLength(5);
      expect(toggle).toHaveAttribute('aria-pressed', 'true');
      fireEvent.click(toggle);
      expect(screen.getAllByTestId('series-title')).toHaveLength(2);
      expect(toggle).toHaveAttribute('aria-pressed', 'false');
    });

    it('does NOT render the toggle when there are no all_complete groups', () => {
      const onlyActionable = [groups[0]!, groups[1]!];
      const ref = createRef<HTMLDivElement>();
      render(wrap(<ScanDecisionsCard
        groups={onlyActionable} totalSeasons={2} outcome="all"
        expanded={new Set()}
        isPending={false} isFetchingNext={false}
        sentinelRef={ref as React.RefObject<HTMLDivElement>}
        onOutcomeChange={vi.fn()} onToggleSeries={vi.fn()} onOpenDecision={vi.fn()}
      />));
      expect(screen.queryByTestId('scan-decisions-skip-toggle')).toBeNull();
    });
  });
});
