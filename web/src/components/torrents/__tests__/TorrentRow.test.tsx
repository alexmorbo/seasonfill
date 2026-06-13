import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { TorrentRow } from '../TorrentRow';
import type { TorrentRow as TorrentRowDTO } from '@/api/seriesTorrents';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider>{node}</TooltipProvider>
    </I18nextProvider>,
  );
}

const base: TorrentRowDTO = {
  hash: 'abc',
  name: 'Show.S05.1080p.WEB-DL.H264-GROUP',
  added_on: new Date(Date.now() - 2 * 86_400_000).toISOString(),
  size_bytes: 8_589_934_592,
  progress: 0.45,
  state_group: 'downloading',
  state_raw: 'downloading',
  dl_speed_bps: 2_200_000,
  up_speed_bps: 800_000,
  eta_seconds: 720,
  ratio: 0.43,
  popularity: 1.24,
  num_seeds: 12,
  num_leechs: 3,
  live: true,
  present: true,
  tracker_host: 'rutracker.org',
};

describe('<TorrentRow />', () => {
  it('renders name, size, progress %, status chip', () => {
    r(<TorrentRow row={base} />);
    expect(screen.getByTestId('torrent-row')).toBeInTheDocument();
    expect(screen.getByTestId('row-name').textContent).toMatch(/Show\.S05/);
    expect(screen.getByTestId('torrent-state-chip').getAttribute('data-state')).toBe('downloading');
  });

  it('tints opacity-50 and swaps the chip on deleted rows', () => {
    r(<TorrentRow row={{ ...base, present: false, live: false }} />);
    const row = screen.getByTestId('torrent-row');
    expect(row.getAttribute('data-deleted')).toBe('true');
    expect(row.className).toMatch(/opacity-50/);
    expect(screen.getByTestId('torrent-state-chip').getAttribute('data-state')).toBe('deleted');
  });

  it('mutes live cells when live=false but present=true', () => {
    r(<TorrentRow row={{ ...base, live: false }} />);
    expect(screen.getByTestId('row-seeds').textContent).toBe('—');
    expect(screen.getByTestId('row-peers').textContent).toBe('—');
    expect(screen.getByTestId('speed-cell-down').textContent).toBe('—');
  });
});
