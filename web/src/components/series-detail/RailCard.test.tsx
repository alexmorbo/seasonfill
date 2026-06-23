import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { RailCard } from './RailCard';
import type { SeriesHero } from '@/api/series';

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

  it('renders network/studio/countries rows when data present', () => {
    const hero: SeriesHero = {
      title: 'X',
      networks: [{ id: 1, name: 'AppleTV+', logo_asset: 'h' }],
      studio: 'Sony Pictures TV',
      countries: ['US'],
      premiere_date: '2026-05-28',
      original_language: 'en',
    };
    render(withI18n(<RailCard
      status="ended"
      hero={hero}
    />));
    expect(screen.getByTestId('rail-row-network')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-studio')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-premiere-date')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-countries')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-original-language')).toBeInTheDocument();
    // B-36: awards relocated to <AwardsBlock /> — RailCard no longer
    // renders the row regardless of input.
    expect(screen.queryByTestId('rail-row-awards')).toBeNull();
  });

  it('hides the studio row when hero.studio is missing', () => {
    const hero: SeriesHero = { title: 'X', countries: ['US'] };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    expect(screen.queryByTestId('rail-row-studio')).toBeNull();
    expect(screen.getByTestId('rail-row-countries')).toBeInTheDocument();
  });

  it('renders network row with logo only (no text) when logo_asset is present', () => {
    const hero: SeriesHero = {
      title: 'X',
      networks: [{ id: 1, name: 'Apple TV', logo_asset: 'abc' }],
    };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    const row = screen.getByTestId('rail-row-network');
    expect(row.querySelector('img')).toBeInTheDocument();
    // No mono-text fallback span when logo is present.
    expect(row.querySelector('span.font-mono')).toBeNull();
  });

  it('renders network row with text fallback when logo_asset is absent', () => {
    const hero: SeriesHero = {
      title: 'X',
      networks: [{ id: 1, name: 'NoLogo Network' }],
    };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    const row = screen.getByTestId('rail-row-network');
    expect(row.querySelector('img')).toBeNull();
    expect(row.querySelector('span.font-mono')).toBeInTheDocument();
    expect(row).toHaveTextContent('NoLogo Network');
  });

  it('falls back to singular country when countries[] is absent', () => {
    const hero: SeriesHero = { title: 'X', country: 'US' };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    expect(screen.getByTestId('rail-row-countries')).toBeInTheDocument();
  });

  it('hides countries row when both countries[] and country are absent', () => {
    const hero: SeriesHero = { title: 'X' };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    expect(screen.queryByTestId('rail-row-countries')).toBeNull();
  });

  it('renders the plural label when countries[] has 2+ entries', () => {
    const hero: SeriesHero = { title: 'X', countries: ['US', 'CA', 'GB'] };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    const row = screen.getByTestId('rail-row-countries');
    // Three country names → render contains commas (CLDR separator).
    expect(row.querySelector('[data-testid="rail-row-countries-value"]')).not.toBeNull();
    expect(row.textContent ?? '').toMatch(/,/);
  });

  it('hides premiere date row when hero.premiere_date is missing', () => {
    const hero: SeriesHero = { title: 'X' };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    expect(screen.queryByTestId('rail-row-premiere-date')).toBeNull();
  });

  it('hides original language row when hero.original_language is missing', () => {
    const hero: SeriesHero = { title: 'X' };
    render(withI18n(<RailCard status="ended" hero={hero} />));
    expect(screen.queryByTestId('rail-row-original-language')).toBeNull();
  });

  it('never renders awards row (B-36: relocated to <AwardsBlock />)', () => {
    const hero: SeriesHero = { title: 'X' };
    // `awards` is no longer in RailCardProps — passing it would TS-fail.
    // This guards the runtime invariant in case the type drifts.
    render(withI18n(<RailCard status="ended" hero={hero} omdbDegraded />));
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
