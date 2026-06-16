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

  it('renders the S{NN} chip in the secondary line when season_number is set', () => {
    r(<TorrentRow row={{ ...base, season_number: 5 }} />);
    const meta = screen.getByTestId('torrent-row').querySelector('.text-tx-muted');
    expect(meta?.textContent).toMatch(/S05/);
  });

  it('omits the season chip when season_number is missing (pack torrent)', () => {
    // The DTO uses omitempty on the Go side, so a pack torrent
    // (no parseable season) arrives as a missing property in
    // TypeScript — equivalent to undefined for the rendering path.
    const row = { ...base };
    delete (row as { season_number?: number }).season_number;
    r(<TorrentRow row={row} />);
    const meta = screen.getByTestId('torrent-row').querySelector('.text-tx-muted');
    // Only the tracker shows on the secondary line — no SxxExx prefix.
    expect(meta?.textContent ?? '').not.toMatch(/^S\d/);
    expect(meta?.textContent).toMatch(/rutracker\.org/);
  });

  it('omits the season chip when season_number is zero (defensive)', () => {
    // seasonLabel short-circuits on n <= 0 even if the wire delivers a
    // 0 — protecting the UI from a hypothetical "Season 0 Specials"
    // edge case the backend would more correctly report as nil.
    r(<TorrentRow row={{ ...base, season_number: 0 }} />);
    const meta = screen.getByTestId('torrent-row').querySelector('.text-tx-muted');
    expect(meta?.textContent ?? '').not.toMatch(/^S\d/);
  });

  it('declares 8 grid tracks at @max-[1280px] so all 8 visible cells fit on row 1 (regression: B-13 #370)', () => {
    // At container widths 1024 ≤ w < 1280 the Ratio cell hides
    // (@max-[1280px]:hidden) but Name, Added, Size, Progress, Status,
    // Seeds/Peers, Speeds, ETA all remain — eight visible children.
    // If the grid-cols declares only 7 tracks the 8th cell auto-flows
    // into an implicit row 2 and visually doubles the row height.
    r(<TorrentRow row={base} />);
    const row = screen.getByTestId('torrent-row');
    const cls = row.className;
    // Track count is encoded in the underscore-separated track list.
    const m = cls.match(/@max-\[1280px\]:grid-cols-\[([^\]]+)\]/);
    expect(m, 'expected @max-[1280px]:grid-cols-[...] class').not.toBeNull();
    const tracks = (m![1] ?? '').split('_');
    expect(tracks).toHaveLength(8);
  });
});
