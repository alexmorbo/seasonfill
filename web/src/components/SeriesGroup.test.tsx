import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { SeriesGroup } from './SeriesGroup';
import { TooltipProvider } from '@/components/ui/tooltip';
import type { SeriesGroup as SeriesGroupModel } from '@/lib/decision-grouping';
import type { Decision } from '@/lib/decisions';
import { DtoDecisionCategory, DtoDecisionDecision } from '@/api/schema';

const dec = (id: string, season: number, cat: Decision['category'] = DtoDecisionCategory.all_complete): Decision => ({
  id, instance: 'alpha', scan_run_id: 'run-1', decision: DtoDecisionDecision.skip,
  reason: 'skip_no_missing', category: cat, season_number: season,
  created_at: new Date().toISOString(),
});

const buildGroup = (over: Partial<SeriesGroupModel> = {}): SeriesGroupModel => ({
  seriesId: 1, seriesTitle: 'Severance', worstCategory: 'all_complete', firstSeenIndex: 0,
  seasons: [
    { seasonNumber: 1, decision: dec('d1', 1) },
    { seasonNumber: 2, decision: dec('d2', 2) },
  ],
  ...over,
});

// Local provider with delayDuration=0 keeps tooltip-open semantics
// in unit tests synchronous-ish (a hover() → await findByRole opens
// without the production 150ms wait). The provider in production
// lives in main.tsx; component tests bring their own.
const renderG = (props: Partial<React.ComponentProps<typeof SeriesGroup>> = {}) =>
  render(
    <TooltipProvider delayDuration={0}>
      <SeriesGroup
        group={props.group ?? buildGroup()}
        expanded={props.expanded ?? false}
        onToggle={props.onToggle ?? vi.fn()}
        onOpenDecision={props.onOpenDecision ?? vi.fn()}
      />
    </TooltipProvider>,
  );

describe('<SeriesGroup />', () => {
  it('renders header with title, chip, and season count', () => {
    renderG();
    expect(screen.getByTestId('series-title')).toHaveTextContent('Severance');
    expect(screen.getByLabelText(/category: all complete/i)).toBeInTheDocument();
    expect(screen.getByText(/2 seasons/i)).toBeInTheDocument();
  });
  it('hides season rows when collapsed (default for all_complete)', () => {
    renderG();
    expect(screen.queryByLabelText(/seasons for severance/i)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument();
  });
  it('shows season rows when expanded; toggle button fires callback', async () => {
    const onToggle = vi.fn();
    renderG({
      group: buildGroup({ worstCategory: 'error', seasons: [{ seasonNumber: 1, decision: dec('d-err', 1, DtoDecisionCategory.error) }] }),
      expanded: true, onToggle,
    });
    expect(screen.getByLabelText(/seasons for severance/i)).toBeInTheDocument();
    expect(screen.getByText('S01')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { expanded: true }));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });
  it('clicking the season open-button calls onOpenDecision(id)', async () => {
    const onOpen = vi.fn();
    renderG({ expanded: true, onOpenDecision: onOpen });
    await userEvent.click(screen.getByRole('button', { name: /open decision for severance season 1/i }));
    expect(onOpen).toHaveBeenCalledWith('d1');
  });
  it('renders an error trigger button that opens a tooltip with full error_detail on hover', async () => {
    const longErr = 'sonarr: 503 service unavailable — '.repeat(10);
    const errDec: Decision = {
      ...dec('d-err', 1, DtoDecisionCategory.error),
      reason: 'error_fetch_releases',
      error_detail: longErr,
    };
    renderG({
      group: buildGroup({
        worstCategory: 'error',
        seasons: [{ seasonNumber: 1, decision: errDec }],
      }),
      expanded: true,
    });

    const trigger = screen.getByTestId('series-row-error-icon');
    expect(trigger).toBeInTheDocument();
    // Trigger is now a focusable <button>, not a <span>.
    expect(trigger.tagName).toBe('BUTTON');
    // aria-label carries the FULL error (no 120-char trim).
    expect(trigger).toHaveAttribute('aria-label', `Error: ${longErr}`);
    // Native `title` attribute is gone — sanity-check we didn't leak it.
    expect(trigger).not.toHaveAttribute('title');

    // Hover the trigger; Radix portals the tooltip into document.body.
    await userEvent.hover(trigger);
    const tip = await screen.findByRole('tooltip');
    // Full error rendered (not truncated). Use textContent because the
    // tooltip body is the only descendant.
    expect(tip).toHaveTextContent('sonarr: 503 service unavailable');
    expect(tip.textContent ?? '').toContain(longErr.trim().slice(0, 200));
  });

  it('opens the error tooltip on keyboard focus (a11y)', async () => {
    const errDec: Decision = {
      ...dec('d-err', 1, DtoDecisionCategory.error),
      reason: 'error_fetch_releases',
      error_detail: 'sonarr: 502 bad gateway',
    };
    renderG({
      group: buildGroup({
        worstCategory: 'error',
        seasons: [{ seasonNumber: 1, decision: errDec }],
      }),
      expanded: true,
    });

    // Focus the trigger directly — keyboard users get the tooltip too.
    const trigger = screen.getByTestId('series-row-error-icon');
    trigger.focus();
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('sonarr: 502 bad gateway');
  });

  it('does not render an error icon on non-error rows', () => {
    renderG({ expanded: true });
    expect(screen.queryByTestId('series-row-error-icon')).not.toBeInTheDocument();
  });

  it('renders superseded rows with strikethrough', () => {
    const supersededDec: Decision = {
      ...dec('d-superseded', 1, DtoDecisionCategory.nothing_found),
      superseded_by_id: '11111111-2222-3333-4444-555555555555',
    };
    renderG({
      group: buildGroup({
        seasons: [{ seasonNumber: 1, decision: supersededDec }],
      }),
      expanded: true,
    });
    const row = screen.getByTestId('series-row-superseded');
    expect(row).toBeInTheDocument();
    expect(row.className).toContain('line-through');
    expect(row.className).toContain('opacity-60');
  });

  it('renders live rows without strikethrough', () => {
    renderG({ expanded: true });
    expect(screen.queryByTestId('series-row-superseded')).not.toBeInTheDocument();
    expect(screen.getAllByTestId('series-row')).toHaveLength(2);
  });
});
