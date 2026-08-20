import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { MovieHeroLibraryStrip } from './MovieHeroLibraryStrip';
import type { MovieDetailLibrary } from '@/api/movies';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('MovieHeroLibraryStrip', () => {
  it('renders the empty state when the movie is in no library', () => {
    render(withI18n(<MovieHeroLibraryStrip library={[]} />));
    expect(screen.getByTestId('movie-detail-library-empty')).toBeInTheDocument();
  });

  it('renders a row per instance with instance name, monitored and hasfile chips', () => {
    const library: MovieDetailLibrary[] = [
      { instance_name: 'radarr', monitored: true, has_file: true, availability: 'released' },
    ];
    render(withI18n(<MovieHeroLibraryStrip library={library} />));

    expect(screen.getByTestId('movie-library-row-radarr')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-monitored')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-hasfile')).toBeInTheDocument();
    expect(screen.getByText('Released')).toBeInTheDocument();
    expect(screen.queryByTestId('movie-detail-library-empty')).toBeNull();
  });

  it('localizes the availability label case-insensitively and falls back to the raw value when unmapped', () => {
    const library: MovieDetailLibrary[] = [
      { instance_name: 'radarr', monitored: true, has_file: true, availability: 'inCinemas' },
      { instance_name: 'radarr-4k', monitored: false, has_file: false, availability: 'someUnknownState' },
    ];
    render(withI18n(<MovieHeroLibraryStrip library={library} />));

    expect(screen.getByText('In cinemas')).toBeInTheDocument();
    expect(screen.getByText('someUnknownState')).toBeInTheDocument();
  });

  it('renders quality/codec chips only when has_file and quality are present', () => {
    const library: MovieDetailLibrary[] = [
      {
        instance_name: 'radarr',
        monitored: true,
        has_file: true,
        availability: 'released',
        quality: 'Bluray-1080p',
        video_codec: 'x265',
        audio_codec: 'EAC3',
      },
    ];
    render(withI18n(<MovieHeroLibraryStrip library={library} />));

    expect(screen.getByTestId('movie-library-quality')).toHaveTextContent('Bluray-1080p');
    expect(screen.getByTestId('movie-library-codec')).toHaveTextContent('x265 · EAC3');
  });

  it('omits quality/codec chips when has_file is true but quality is missing', () => {
    const library: MovieDetailLibrary[] = [
      { instance_name: 'radarr', monitored: true, has_file: true, availability: 'released' },
    ];
    render(withI18n(<MovieHeroLibraryStrip library={library} />));

    expect(screen.queryByTestId('movie-library-quality')).toBeNull();
    expect(screen.queryByTestId('movie-library-codec')).toBeNull();
  });

  it('renders the size chip only when size_on_disk_bytes is positive', () => {
    const library: MovieDetailLibrary[] = [
      {
        instance_name: 'radarr', monitored: true, has_file: true, size_on_disk_bytes: 12_400_000_000,
      },
    ];
    render(withI18n(<MovieHeroLibraryStrip library={library} />));
    expect(screen.getByTestId('movie-library-size').textContent).toMatch(/GB/);
  });

  it('renders one row per instance when the movie is known to multiple Radarr instances', () => {
    const library: MovieDetailLibrary[] = [
      { instance_name: 'radarr', monitored: true, has_file: true },
      { instance_name: 'radarr-4k', monitored: false, has_file: false },
    ];
    render(withI18n(<MovieHeroLibraryStrip library={library} />));

    expect(screen.getByTestId('movie-library-row-radarr')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-row-radarr-4k')).toBeInTheDocument();
  });
});
