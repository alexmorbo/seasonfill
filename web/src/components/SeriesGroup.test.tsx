import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { SeriesGroup } from './SeriesGroup';
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

const renderG = (props: Partial<React.ComponentProps<typeof SeriesGroup>> = {}) =>
  render(
    <SeriesGroup
      group={props.group ?? buildGroup()}
      expanded={props.expanded ?? false}
      onToggle={props.onToggle ?? vi.fn()}
      onOpenDecision={props.onOpenDecision ?? vi.fn()}
    />,
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
});
