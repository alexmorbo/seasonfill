import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { SeriesHero } from './SeriesHero';

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient();
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

const baseHero = {
  title: 'For All Mankind',
  status: 'continuing',
  year_start: 2019,
  runtime_minutes: 45,
  genres: [{ id: 1, name: 'Drama', language: 'en-US' }],
  networks: [{ id: 1, name: 'AppleTV+' }],
  backdrop_asset: 'fake-hash',
  poster_asset: 'fake-poster',
  studio: 'Sony Pictures TV',
  country: 'US',
};

describe('SeriesHero v2 bleed', () => {
  it('does NOT render a StatusPill in the title row', () => {
    render(wrap(<SeriesHero instance="homelab" seriesId={369} hero={baseHero as any} />));
    expect(screen.queryByTestId('status-pill')).toBeNull();
  });

  it('does NOT render the networks strip in the meta row', () => {
    render(wrap(<SeriesHero instance="homelab" seriesId={369} hero={baseHero as any} />));
    expect(screen.queryByText(/AppleTV\+/i)).toBeNull();
  });

  it('renders the glass NextEpisodeCard wrapper', () => {
    const heroWithNext = {
      ...baseHero,
      next_episode: { season_number: 5, episode_number: 3, title: 'Glasnost',
        air_date: new Date(Date.now() + 4*86400_000).toISOString() },
    };
    render(wrap(<SeriesHero instance="homelab" seriesId={369} hero={heroWithNext as any} />));
    expect(screen.getByTestId('hero-next-wrap')).toBeInTheDocument();
    expect(screen.getByTestId('next-episode-card').dataset['variant']).toBe('default');
  });

  it('renders the HeroLibraryStrip with dark tone over the scrim', () => {
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={baseHero as any}
      library={{ monitored: true, episodes_total: 48, episodes_on_disk: 42,
                 missing_count: 6, size_on_disk_bytes: 12_000_000_000, dominant_quality: '' }}
    />));
    const strip = screen.getByTestId('hero-library-strip');
    expect(strip.dataset['tone']).toBe('dark');
  });

  it('falls back to sonarr-only flat header (no scrim layer)', () => {
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={{ title: 'Cold', status: 'unknown' } as any}
    />));
    expect(screen.getByTestId('series-hero').dataset['fallback']).toBe('sonarr-only');
    expect(screen.queryByTestId('hero-scrim')).toBeNull();
  });
});
