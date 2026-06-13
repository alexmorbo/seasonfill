import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { TorrentStateChip } from '../TorrentStateChip';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider>{node}</TooltipProvider>
    </I18nextProvider>,
  );
}

describe('<TorrentStateChip />', () => {
  it.each([
    'downloading', 'seeding', 'stalled', 'queued', 'paused', 'checking', 'error', 'unknown',
  ] as const)('renders the %s group with the right data-state', (g) => {
    r(<TorrentStateChip group={g} />);
    expect(screen.getByTestId('torrent-state-chip').getAttribute('data-state')).toBe(g);
  });

  it('falls back to unknown when the group token is unrecognised', () => {
    r(<TorrentStateChip group="ufo" />);
    expect(screen.getByTestId('torrent-state-chip').getAttribute('data-state')).toBe('unknown');
  });

  it('renders a deleted chip with the date when deleted=true', () => {
    r(<TorrentStateChip group="seeding" deleted deletedAt="2026-04-03T10:00:00Z" />);
    const chip = screen.getByTestId('torrent-state-chip');
    expect(chip.getAttribute('data-state')).toBe('deleted');
    expect(chip.textContent).toMatch(/Apr/);
  });
});
