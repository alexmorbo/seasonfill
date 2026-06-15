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

  it('renders percent + counts + size chips', () => {
    render(withI18n(<HeroLibraryStrip library={{
      monitored: true,
      episodes_total: 48,
      episodes_on_disk: 42,
      missing_count: 6,
      size_on_disk_bytes: 12_400_000_000,
      dominant_quality: 'WEB-DL 1080p',
    }} />));
    expect(screen.getByTestId('hero-library-counts').textContent).toMatch(/42\/48/);
    expect(screen.getByTestId('hero-library-size').textContent).toMatch(/GB/);
    expect(screen.getByTestId('hero-library-missing')).toBeInTheDocument();
  });

  it('fires onDownloadClick when the download chip is activated', () => {
    const onClick = vi.fn();
    render(withI18n(<HeroLibraryStrip
      library={{
        monitored: true,
        episodes_total: 48,
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
      monitored: true, episodes_total: 1, episodes_on_disk: 1,
      missing_count: 0, size_on_disk_bytes: 1, dominant_quality: '',
    }} />));
    expect(screen.getByTestId('hero-library-strip').dataset['tone']).toBe('light');
  });
});
