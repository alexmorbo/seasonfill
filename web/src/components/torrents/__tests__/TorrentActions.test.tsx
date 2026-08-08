import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { TorrentActions } from '../TorrentActions';

// Mock the mutation hook so no QueryClient/network is needed and we can
// assert exactly what the component calls.
const mutate = vi.fn();
vi.mock('@/lib/torrent-mutations', () => ({
  useTorrentAction: () => ({ mutate, isPending: false }),
}));

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider>{node}</TooltipProvider>
    </I18nextProvider>,
  );
}

const props = { instance: 'main', hash: 'abc123' } as const;

describe('<TorrentActions />', () => {
  beforeEach(() => {
    mutate.mockClear();
  });

  it('resume fires the mutation directly with no dialog', () => {
    r(<TorrentActions {...props} health="ok" />);
    expect(screen.queryByTestId('torrent-confirm-dialog')).toBeNull();
    fireEvent.click(screen.getByTestId('torrent-action-resume'));
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(mutate).toHaveBeenCalledWith(
      { instance: 'main', hash: 'abc123', action: 'resume' },
    );
  });

  it('pause opens a confirm dialog and only mutates on confirm', () => {
    r(<TorrentActions {...props} health="ok" />);
    fireEvent.click(screen.getByTestId('torrent-action-pause'));
    // Dialog visible, nothing mutated yet.
    expect(screen.getByTestId('torrent-confirm-dialog')).toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();
    // Confirm → mutate with pause.
    fireEvent.click(screen.getByTestId('torrent-confirm-submit'));
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(mutate.mock.calls[0]![0]).toEqual(
      { instance: 'main', hash: 'abc123', action: 'pause' },
    );
  });

  it('recheck opens a confirm dialog before mutating', () => {
    r(<TorrentActions {...props} health="ok" />);
    fireEvent.click(screen.getByTestId('torrent-action-recheck'));
    expect(screen.getByTestId('torrent-confirm-dialog')).toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('torrent-confirm-submit'));
    expect(mutate.mock.calls[0]![0]).toEqual(
      { instance: 'main', hash: 'abc123', action: 'recheck' },
    );
  });

  it('renders the health badge variant + text for ok / stalled / error', () => {
    const { rerender } = r(<TorrentActions {...props} health="ok" />);
    const badge = () => screen.getByTestId('torrent-health');
    expect(badge().getAttribute('data-health')).toBe('ok');
    expect(badge().className).toMatch(/text-ok/);

    rerender(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider><TorrentActions {...props} health="stalled" /></TooltipProvider>
      </I18nextProvider>,
    );
    expect(badge().getAttribute('data-health')).toBe('stalled');
    expect(badge().className).toMatch(/text-warn/);

    rerender(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider><TorrentActions {...props} health="error" /></TooltipProvider>
      </I18nextProvider>,
    );
    expect(badge().getAttribute('data-health')).toBe('error');
    expect(badge().className).toMatch(/text-danger/);
  });

  it('renders no health badge when health is undefined', () => {
    r(<TorrentActions {...props} />);
    expect(screen.queryByTestId('torrent-health')).toBeNull();
  });
});
