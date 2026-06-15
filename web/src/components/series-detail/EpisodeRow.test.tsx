import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { EpisodeRow } from './EpisodeRow';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<EpisodeRow />', () => {
  it('renders code, title, runtime and a have-dot for on-disk episode', () => {
    r(<EpisodeRow episode={{
      episode_number: 3, title: 'Glasnost', air_date: '2026-07-14',
      runtime_minutes: 45, has_file: true, monitored: true, quality: 'WEB-DL 1080p',
    }} />);
    expect(screen.getByText('S00E03')).toBeInTheDocument();
    expect(screen.getByText('Glasnost')).toBeInTheDocument();
    expect(screen.getByText(/45m/)).toBeInTheDocument();
    expect(screen.getByTestId('episode-dot-have')).toBeInTheDocument();
    expect(screen.getByText('WEB-DL 1080p')).toBeInTheDocument();
  });

  it('shows the missing dot for an aired+monitored+no-file episode', () => {
    const yesterday = new Date(Date.now() - 86_400_000).toISOString();
    r(<EpisodeRow episode={{
      episode_number: 4, title: 'Lost', air_date: yesterday,
      has_file: false, monitored: true,
    }} />);
    expect(screen.getByTestId('episode-dot-missing')).toBeInTheDocument();
  });

  it('shows the unmonitored dot for an unmonitored episode', () => {
    r(<EpisodeRow episode={{
      episode_number: 5, title: 'Cut', has_file: false, monitored: false,
    }} />);
    expect(screen.getByTestId('episode-dot-unmonitored')).toBeInTheDocument();
  });

  it('renders the finale badge when finale_type is set', () => {
    r(<EpisodeRow episode={{
      episode_number: 10, title: 'Goodbye', finale_type: 'season', has_file: true,
    }} />);
    expect(screen.getByTestId('episode-finale')).toBeInTheDocument();
  });

  it('toggles overview clamp on click', () => {
    r(<EpisodeRow episode={{
      episode_number: 1, title: 'A', overview: 'long text', has_file: false,
    }} />);
    const ov = screen.getByTestId('episode-overview');
    expect(ov.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(ov);
    expect(ov.getAttribute('aria-expanded')).toBe('true');
  });

  it('renders the .eq chip with combined codec line when present', () => {
    r(<EpisodeRow episode={{
      episode_number: 1,
      has_file: true,
      monitored: true,
      quality: 'WEB-DL 1080p',
      video_codec: 'HEVC',
      audio_codec: 'DDP',
      audio_channels: '5.1',
      release_group: 'RARBG',
    } as any} />);
    const chip = screen.getByTestId('episode-row-eq');
    expect(chip.textContent).toBe('WEB-DL 1080p · HEVC · DD+ 5.1 · RARBG');
  });

  it('suppresses the .eq chip when episode has no file', () => {
    r(<EpisodeRow episode={{
      episode_number: 1, has_file: false, monitored: true,
    } as any} />);
    expect(screen.queryByTestId('episode-row-eq')).toBeNull();
  });

  it('suppresses the .eq chip when no media-meta fields are populated', () => {
    r(<EpisodeRow episode={{
      episode_number: 1, has_file: true, monitored: true,
    } as any} />);
    expect(screen.queryByTestId('episode-row-eq')).toBeNull();
  });
});
