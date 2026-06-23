import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { TooltipProvider } from '@/components/ui/tooltip';
import { QueueSeasonChips } from './QueueSeasonChips';
import type { SeasonEpisodePresence } from '@/lib/missing';

const eps: SeasonEpisodePresence[] = [
  { number: 1, title: 'Solaricks', present: true },
  { number: 2, title: 'The Jerrick Trick', present: false },
  { number: 3, title: '', present: false },
];

function ProvidersWrap({ children }: { children: React.ReactNode }) {
  // 0 delay so the tooltip mounts synchronously on hover.
  return <TooltipProvider delayDuration={0}>{children}</TooltipProvider>;
}

describe('<QueueSeasonChips />', () => {
  it('renders one chip per episode with correct data-present', () => {
    renderWithProviders(
      <ProvidersWrap>
        <QueueSeasonChips seasonNumber={9} episodes={eps} />
      </ProvidersWrap>,
    );
    const list = screen.getByTestId('queue-season-chips');
    expect(list.getAttribute('data-season-number')).toBe('9');
    expect(list.children.length).toBe(3);
    expect(screen.getByText('E1').getAttribute('data-present')).toBe('true');
    expect(screen.getByText('E2').getAttribute('data-present')).toBe('false');
    expect(screen.getByText('E3').getAttribute('data-present')).toBe('false');
  });

  it('uses ok-dim color for present and warn-dim for missing chips', () => {
    renderWithProviders(
      <ProvidersWrap>
        <QueueSeasonChips seasonNumber={1} episodes={eps} />
      </ProvidersWrap>,
    );
    expect(screen.getByText('E1').className).toMatch(/bg-ok-dim/);
    expect(screen.getByText('E2').className).toMatch(/bg-warn-dim/);
  });

  it('shows the episode title in the tooltip on hover', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <ProvidersWrap>
        <QueueSeasonChips seasonNumber={9} episodes={eps} />
      </ProvidersWrap>,
    );
    await user.hover(screen.getByText('E1'));
    // Radix portals tooltip content; find by accessible text.
    const tooltip = await screen.findAllByText(/Episode 1: Solaricks/i);
    expect(tooltip.length).toBeGreaterThan(0);
  });

  it('falls back to plain "Episode N" tooltip when title is empty', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <ProvidersWrap>
        <QueueSeasonChips seasonNumber={9} episodes={eps} />
      </ProvidersWrap>,
    );
    await user.hover(screen.getByText('E3'));
    const tooltip = await screen.findAllByText(/^Episode 3$/i);
    expect(tooltip.length).toBeGreaterThan(0);
  });

  it('renders nothing visible when episodes=[]', () => {
    renderWithProviders(
      <ProvidersWrap>
        <QueueSeasonChips seasonNumber={1} episodes={[]} />
      </ProvidersWrap>,
    );
    const list = screen.getByTestId('queue-season-chips');
    expect(list.children.length).toBe(0);
  });
});
