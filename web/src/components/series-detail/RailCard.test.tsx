import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { RailCard } from './RailCard';
import type { SeriesHero } from '@/api/seriesDetail';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('RailCard', () => {
  it('renders the status row in accent color when continuing', () => {
    const hero: SeriesHero = { title: 'X' };
    render(withI18n(<RailCard status="continuing" hero={hero} />));
    const row = screen.getByTestId('rail-row-status');
    expect(row.querySelector('.text-accent')).toBeTruthy();
  });

  it('renders network/studio/country/awards rows when data present', () => {
    const hero: SeriesHero = {
      title: 'X',
      networks: [{ id: 1, name: 'AppleTV+', logo_asset: 'h' }],
      studio: 'Sony Pictures TV',
      country: 'US',
    };
    render(withI18n(<RailCard
      status="ended"
      hero={hero}
      awards="4 wins · 18 nominations"
    />));
    expect(screen.getByTestId('rail-row-network')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-studio')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-country')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-awards')).toBeInTheDocument();
  });

  it('hides the studio row when hero.studio is missing', () => {
    const hero: SeriesHero = { title: 'X', country: 'US' };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    expect(screen.queryByTestId('rail-row-studio')).toBeNull();
    expect(screen.getByTestId('rail-row-country')).toBeInTheDocument();
  });

  it('hides the awards row when omdb is degraded', () => {
    const hero: SeriesHero = { title: 'X' };
    render(withI18n(<RailCard
      status="ended"
      hero={hero}
      awards="4 wins"
      omdbDegraded
    />));
    expect(screen.queryByTestId('rail-row-awards')).toBeNull();
  });

  it('renders keyword chips when keywords prop has items', () => {
    const hero: SeriesHero = { title: 'X' };
    render(withI18n(<RailCard
      status="continuing"
      hero={hero}
      keywords={[{ id: 1, name: 'space race', language: 'en-US' }]}
    />));
    expect(screen.getByTestId('rail-keywords')).toBeInTheDocument();
    expect(screen.getByText('space race')).toBeInTheDocument();
  });

  it('applies sticky positioning class on desktop', () => {
    const hero: SeriesHero = { title: 'X' };
    render(withI18n(<RailCard status="continuing" hero={hero} />));
    expect(screen.getByTestId('rail-card').className).toMatch(/lg:sticky/);
  });
});
