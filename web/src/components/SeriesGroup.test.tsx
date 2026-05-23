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
  it('renders an error icon with title preview on error rows', async () => {
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
    const icon = screen.getByTestId('series-row-error-icon');
    expect(icon).toBeInTheDocument();
    const title = icon.getAttribute('title') ?? '';
    expect(title.length).toBeLessThanOrEqual(123); // 120 + "..."
    expect(title).toContain('sonarr: 503');
    expect(title.endsWith('...')).toBe(true);
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
