import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { DecisionDetail } from './DecisionDetail';
import type { Decision } from '@/lib/decisions';
import { DtoDecisionCategory, DtoDecisionDecision } from '@/api/schema';

function dec(over: Partial<Decision> = {}): Decision {
  return {
    id: 'd_001',
    instance: 'alpha',
    series_title: 'Severance',
    season_number: 1,
    decision: DtoDecisionDecision.grab,
    reason: 'grab_selected_dry_run',
    category: DtoDecisionCategory.action_taken,
    candidates_count: 3,
    releases_found: 10,
    existing_count: 1,
    missing_count: 8,
    selected_guid: 'g-1',
    dry_run_would_grab: true,
    scan_run_id: 'run-1',
    created_at: new Date().toISOString(),
    ...over,
  };
}

describe('<DecisionDetail /> category chip', () => {
  it('renders the category chip with the correct label for action_taken', () => {
    render(<DecisionDetail d={dec()} />);
    const chip = screen.getByLabelText(/category: action taken/i);
    expect(chip).toBeInTheDocument();
    expect(chip).toHaveAttribute('data-category', 'action_taken');
  });

  it('renders all_complete chip for healthy series', () => {
    render(<DecisionDetail d={dec({ category: DtoDecisionCategory.all_complete })} />);
    expect(screen.getByLabelText(/category: all complete/i)).toBeInTheDocument();
  });

  it('falls back to Unknown when category is missing (pre-011a rows)', () => {
    const d = dec();
    // Strip the category to simulate a row from before 011a shipped.
    const { category: _category, ...rest } = d;
    render(<DecisionDetail d={rest as Decision} />);
    const chip = screen.getByLabelText(/category: unknown/i);
    expect(chip).toHaveAttribute('data-category', 'unknown');
  });
});
