import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { StuckHero, _clearStuckDismissedForTests } from './StuckHero';
import type { StuckSeason } from '@/lib/api/decisions';

const items: readonly StuckSeason[] = [
  { seriesId: 1, seriesTitle: 'Foundation', seasonNumber: 3, consecutive: 9,
    lastReason: 'nothing_above_threshold', lastDecisionId: 'd1', lastScanRunId: 's1',
    instance: 'homelab' },
  { seriesId: 2, seriesTitle: 'Silo', seasonNumber: 2, consecutive: 3,
    lastReason: 'no_candidates', lastDecisionId: 'd2', lastScanRunId: 's2',
    instance: 'homelab' },
];

function renderHero(props: Partial<Parameters<typeof StuckHero>[0]> = {}) {
  return render(
    <I18nextProvider i18n={i18n}>
      <StuckHero items={items} isLoading={false} onOpenSeason={vi.fn()} {...props} />
    </I18nextProvider>,
  );
}

describe('StuckHero', () => {
  beforeEach(() => _clearStuckDismissedForTests());

  it('renders one row per stuck season', () => {
    renderHero();
    expect(screen.getByTestId('stuck-hero')).toBeInTheDocument();
    expect(screen.getByText('Foundation')).toBeInTheDocument();
    expect(screen.getByText('Silo')).toBeInTheDocument();
  });

  it('renders nothing when items=[]', () => {
    renderHero({ items: [] });
    expect(screen.queryByTestId('stuck-hero')).not.toBeInTheDocument();
  });

  it('renders skeleton while loading', () => {
    const { container } = renderHero({ items: undefined, isLoading: true });
    // Skeleton instances expected (header icon + title + 3 rows)
    expect(container.querySelectorAll('[data-slot="skeleton"], .animate-pulse').length)
      .toBeGreaterThan(0);
  });

  it('dismiss writes sessionStorage and unmounts the card', async () => {
    renderHero();
    const dismissBtn = screen.getByRole('button', { name: /dismiss|скрыть/i });
    await userEvent.click(dismissBtn);
    expect(screen.queryByTestId('stuck-hero')).not.toBeInTheDocument();
    expect(window.sessionStorage.getItem('seasonfill:decisions:stuckDismissed'))
      .toBe('true');
  });

  it('row click fires onOpenSeason with the stuck row payload', async () => {
    const onOpenSeason = vi.fn();
    renderHero({ onOpenSeason });
    await userEvent.click(screen.getByText('Foundation'));
    expect(onOpenSeason).toHaveBeenCalledWith(
      expect.objectContaining({ seriesId: 1, seasonNumber: 3 }),
    );
  });

  it('does not re-render after a previous dismiss (sessionStorage seeded)', () => {
    window.sessionStorage.setItem('seasonfill:decisions:stuckDismissed', 'true');
    renderHero();
    expect(screen.queryByTestId('stuck-hero')).not.toBeInTheDocument();
  });
});
