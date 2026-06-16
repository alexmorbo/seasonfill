import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { HeroLibraryStrip } from './HeroLibraryStrip';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('HeroLibraryStrip', () => {
  it('renders the empty state when library has no episodes', () => {
    render(withI18n(<HeroLibraryStrip />));
    expect(screen.getByTestId('hero-library-empty')).toBeInTheDocument();
  });

  it('renders percent + counts + size chips with icons on % and missing', () => {
    render(withI18n(<HeroLibraryStrip library={{
      monitored: true,
      episodes_total: 48,
      episodes_aired: 48,
      episodes_on_disk: 42,
      missing_count: 6,
      size_on_disk_bytes: 12_400_000_000,
      dominant_quality: 'WEB-DL 1080p',
    }} />));
    expect(screen.getByTestId('hero-library-counts').textContent).toMatch(/42\/48/);
    expect(screen.getByTestId('hero-library-size').textContent).toMatch(/GB/);

    const pctChip = screen.getByTestId('hero-library-percent');
    expect(pctChip.querySelector('svg')).not.toBeNull();

    const missingChip = screen.getByTestId('hero-library-missing');
    expect(missingChip).toBeInTheDocument();
    expect(missingChip.querySelector('svg')).not.toBeNull();

    // X/Y and size chips stay icon-free.
    expect(screen.getByTestId('hero-library-counts').querySelector('svg')).toBeNull();
    expect(screen.getByTestId('hero-library-size').querySelector('svg')).toBeNull();
  });

  it('fires onDownloadClick when the download chip is activated', () => {
    const onClick = vi.fn();
    render(withI18n(<HeroLibraryStrip
      library={{
        monitored: true,
        episodes_total: 48,
        episodes_aired: 48,
        episodes_on_disk: 42,
        missing_count: 0,
        size_on_disk_bytes: 1024,
        dominant_quality: '',
      }}
      download={{ queue_id: 1, title: 'S05E03', status: 'downloading' }}
      onDownloadClick={onClick}
    />));
    fireEvent.click(screen.getByTestId('hero-library-download'));
    expect(onClick).toHaveBeenCalled();
  });

  it('emits data-tone for downstream styling overrides', () => {
    render(withI18n(<HeroLibraryStrip tone="light" library={{
      monitored: true, episodes_total: 1, episodes_aired: 1, episodes_on_disk: 1,
      missing_count: 0, size_on_disk_bytes: 1, dominant_quality: '',
    }} />));
    expect(screen.getByTestId('hero-library-strip').dataset['tone']).toBe('light');
  });

  // Story 376: prefer episodes_aired as the denominator so unaired
  // future episodes don't depress the headline percentage.
  it('uses episodes_aired as the denominator when present', () => {
    render(withI18n(<HeroLibraryStrip library={{
      monitored: true,
      episodes_total: 40,
      episodes_aired: 38,
      episodes_on_disk: 38,
      missing_count: 0,
      size_on_disk_bytes: 12_400_000_000,
      dominant_quality: 'WEB-DL 1080p',
    }} />));
    expect(screen.getByTestId('hero-library-counts').textContent).toMatch(/38\/38/);
  });

  it('falls back to episodes_total when episodes_aired is 0', () => {
    render(withI18n(<HeroLibraryStrip library={{
      monitored: true,
      episodes_total: 48,
      episodes_aired: 0,
      episodes_on_disk: 42,
      missing_count: 6,
      size_on_disk_bytes: 12_400_000_000,
      dominant_quality: 'WEB-DL 1080p',
    }} />));
    expect(screen.getByTestId('hero-library-counts').textContent).toMatch(/42\/48/);
  });

  it('caps percentage at 100% when episodes_on_disk exceeds episodes_aired', () => {
    render(withI18n(<HeroLibraryStrip library={{
      monitored: true,
      episodes_total: 40,
      episodes_aired: 38,
      episodes_on_disk: 39,
      missing_count: 0,
      size_on_disk_bytes: 12_400_000_000,
      dominant_quality: 'WEB-DL 1080p',
    }} />));
    const html = document.body.innerHTML;
    expect(html).toMatch(/100%/);
  });

  // Story 379: hero in-progress pill from Sonarr queue.
  it('renders the in-progress pill when library.in_progress is set', () => {
    render(withI18n(<HeroLibraryStrip library={{
      monitored: true,
      episodes_total: 48,
      episodes_aired: 48,
      episodes_on_disk: 42,
      missing_count: 0,
      size_on_disk_bytes: 1024,
      dominant_quality: 'WEB-DL 1080p',
      in_progress: { season_number: 5, episode_number: 3, percent: 45, title: 'A Rickconvenient Mort' },
    }} />));
    const pill = screen.getByTestId('hero-library-in-progress');
    expect(pill.textContent).toMatch(/S05E03/);
    expect(pill.textContent).toMatch(/45%/);
  });

  it('omits the in-progress pill when undefined', () => {
    render(withI18n(<HeroLibraryStrip library={{
      monitored: true,
      episodes_total: 1,
      episodes_aired: 1,
      episodes_on_disk: 1,
      missing_count: 0,
      size_on_disk_bytes: 1,
      dominant_quality: '',
    }} />));
    expect(screen.queryByTestId('hero-library-in-progress')).not.toBeInTheDocument();
  });
});
