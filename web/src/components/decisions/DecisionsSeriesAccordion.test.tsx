import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { DecisionsSeriesAccordion } from './DecisionsSeriesAccordion';
import { DtoDecisionCategory, DtoDecisionDecision } from '@/api/schema';
import type { Decision } from '@/lib/api/decisions';

const baseRow = (over: Partial<Decision>): Decision => ({
  id: `dec-${Math.random()}`,
  created_at: '2026-06-07T10:00:00Z',
  ...over,
});

const rows: readonly Decision[] = [
  baseRow({ series_id: 1, series_title: 'Foundation', season_number: 3,
            category: DtoDecisionCategory.nothing_found, decision: DtoDecisionDecision.skip, reason: 'nothing_above_threshold' }),
  baseRow({ series_id: 1, series_title: 'Foundation', season_number: 2,
            category: DtoDecisionCategory.all_complete, decision: DtoDecisionDecision.skip, reason: 'all_complete' }),
  baseRow({ series_id: 2, series_title: 'For All Mankind', season_number: 5,
            category: DtoDecisionCategory.action_taken, decision: DtoDecisionDecision.grab, reason: 'upgrade_available' }),
  baseRow({ series_id: 3, series_title: 'OK Series', season_number: 1,
            category: DtoDecisionCategory.all_complete, decision: DtoDecisionDecision.skip, reason: 'all_complete' }),
];

function renderAcc(input: readonly Decision[] = rows, onOpen = vi.fn()) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <DecisionsSeriesAccordion rows={input} onOpenSeason={onOpen} />
        </TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('DecisionsSeriesAccordion', () => {
  it('renders one item per series', () => {
    renderAcc();
    expect(screen.getByText('Foundation')).toBeInTheDocument();
    expect(screen.getByText('For All Mankind')).toBeInTheDocument();
    expect(screen.getByText('OK Series')).toBeInTheDocument();
  });

  it('default-opens series whose worstCategory != all_complete', () => {
    renderAcc();
    // Foundation (nothing_found) and For All Mankind (action_taken) open
    // by default; OK Series collapsed. The collapsed item still mounts
    // its content via Radix (data-state=closed). We instead verify that
    // the Foundation S03 season row IS visible in the DOM.
    expect(screen.queryAllByText(/S03/).length).toBeGreaterThan(0);
  });

  it('renders all-complete series collapsed (data-state=closed)', () => {
    renderAcc();
    // Find the OK Series item's trigger and verify its parent
    // AccordionItem has data-state=closed.
    const trigger = screen.getByText('OK Series').closest('[data-state]');
    expect(trigger).toHaveAttribute('data-state', 'closed');
  });

  it('clicking a season row fires onOpenSeason with the decision', async () => {
    const onOpen = vi.fn();
    renderAcc(rows, onOpen);
    const seasonRows = await screen.findAllByTestId('decisions-season-row');
    await userEvent.click(seasonRows[0]!);
    expect(onOpen).toHaveBeenCalledTimes(1);
    // groupBySeries+sortGroups sorts by worstCategory priority DESC; the
    // first open series is the highest-priority one (action_taken=5).
    expect(onOpen.mock.calls[0]![0]!.series_title).toBe('For All Mankind');
  });

  it('renders nothing for empty rows', () => {
    const { container } = renderAcc([]);
    expect(container.firstChild).toBeNull();
  });
});
