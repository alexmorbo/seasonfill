import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
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
});
