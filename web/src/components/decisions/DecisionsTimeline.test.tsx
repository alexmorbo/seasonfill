import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DecisionsTimeline } from './DecisionsTimeline';
import { DtoDecisionDecision } from '@/api/schema';
import type { Decision } from '@/lib/api/decisions';

const rows: readonly Decision[] = [
  { id: 'd1', decision: DtoDecisionDecision.grab,             reason: 'upgrade_available',
    created_at: '2026-06-07T19:32:00Z', scan_run_id: '7b3d0001abcd1234' },
  { id: 'd2', decision: DtoDecisionDecision.already_optimal,  reason: 'already_optimal',
    created_at: '2026-06-06T13:21:00Z', scan_run_id: 'a1f27c00deadbeef' },
  { id: 'd3', decision: DtoDecisionDecision.blocked_cooldown, reason: 'blocked_cooldown',
    created_at: '2026-06-06T07:00:00Z', scan_run_id: '9d041100feedcafe' },
];

function renderTL(input: readonly Decision[] = rows) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>
        <DecisionsTimeline rows={input} />
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe('DecisionsTimeline', () => {
  it('renders one node per row', () => {
    renderTL();
    expect(screen.getByTestId('decisions-timeline')).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(3);
  });

  it('marks the grab node with data-variant=grab', () => {
    renderTL();
    const items = screen.getAllByRole('listitem');
    expect(items[0]).toHaveAttribute('data-variant', 'grab');
  });

  it('marks the cooldown node with data-variant=block', () => {
    renderTL();
    const items = screen.getAllByRole('listitem');
    expect(items[2]).toHaveAttribute('data-variant', 'block');
  });

  it('renders scan link with /scans/<id>?drawer= URL', () => {
    renderTL();
    const links = screen.getAllByRole('link');
    expect(links[0]).toHaveAttribute(
      'href',
      '/scans/7b3d0001abcd1234?drawer=d1',
    );
  });

  it('renders empty fallback when rows=[]', () => {
    renderTL([]);
    expect(screen.getByTestId('timeline-empty')).toBeInTheDocument();
  });
});
