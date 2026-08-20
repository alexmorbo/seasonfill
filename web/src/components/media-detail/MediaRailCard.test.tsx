import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { MediaRailCard } from './MediaRailCard';
import type { MediaFact } from './view-model';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('MediaRailCard', () => {
  it('renders the status row in accent color when continuing', () => {
    const facts: MediaFact[] = [
      { id: 'status', label: 'Status', value: 'Continuing', accent: true, testId: 'rail-row-status' },
    ];
    render(withI18n(<MediaRailCard facts={facts} />));
    const row = screen.getByTestId('rail-row-status');
    expect(row.querySelector('.text-accent')).toBeTruthy();
  });

  it('renders network/studio/premiere/countries/original-language rows in the given order', () => {
    const facts: MediaFact[] = [
      { id: 'status', label: 'Status', value: 'Ended', testId: 'rail-row-status' },
      {
        id: 'network', label: 'Network',
        value: <span className="font-mono text-[10.5px]">AppleTV+</span>,
        testId: 'rail-row-network',
      },
      { id: 'studio', label: 'Studio', value: <span data-testid="rail-row-studio-value">Sony Pictures TV</span>, testId: 'rail-row-studio' },
      { id: 'premiere-date', label: 'Premiere', value: '2026-05-28', testId: 'rail-row-premiere-date' },
      {
        id: 'countries', label: 'Country',
        value: <span data-testid="rail-row-countries-value">United States</span>,
        testId: 'rail-row-countries',
      },
      { id: 'original-language', label: 'Original language', value: 'English', testId: 'rail-row-original-language' },
    ];
    render(withI18n(<MediaRailCard facts={facts} />));
    expect(screen.getByTestId('rail-row-network')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-studio')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-premiere-date')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-countries')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-original-language')).toBeInTheDocument();
    // Ordering: assert DOM order matches the facts array order (direct
    // row children only — exclude nested value testids like
    // rail-row-studio-value / rail-row-countries-value).
    const rows = screen.getByTestId('rail-card').querySelectorAll(':scope > div > [data-testid^="rail-row-"]');
    const order = Array.from(rows).map((r) => r.getAttribute('data-testid'));
    expect(order).toEqual([
      'rail-row-status', 'rail-row-network', 'rail-row-studio',
      'rail-row-premiere-date', 'rail-row-countries', 'rail-row-original-language',
    ]);
  });

  it('omits rows the adapter does not push (e.g. no studio fact)', () => {
    const facts: MediaFact[] = [
      { id: 'status', label: 'Status', value: 'Ended', testId: 'rail-row-status' },
      {
        id: 'countries', label: 'Country',
        value: <span data-testid="rail-row-countries-value">United States</span>,
        testId: 'rail-row-countries',
      },
    ];
    render(withI18n(<MediaRailCard facts={facts} />));
    expect(screen.queryByTestId('rail-row-studio')).toBeNull();
    expect(screen.getByTestId('rail-row-countries')).toBeInTheDocument();
  });

  it('renders network row with logo image (no font-mono span) when a logo node is supplied', () => {
    const facts: MediaFact[] = [
      {
        id: 'network', label: 'Network',
        value: <img src="https://example.com/logo.png" alt="Apple TV" />,
        testId: 'rail-row-network',
      },
    ];
    render(withI18n(<MediaRailCard facts={facts} />));
    const row = screen.getByTestId('rail-row-network');
    expect(row.querySelector('img')).toBeInTheDocument();
    expect(row.querySelector('span.font-mono')).toBeNull();
  });

  it('renders network row with a font-mono text fallback when no logo is supplied', () => {
    const facts: MediaFact[] = [
      {
        id: 'network', label: 'Network',
        value: <span className="font-mono text-[10.5px] tracking-[0.08em] uppercase">NoLogo Network</span>,
        testId: 'rail-row-network',
      },
    ];
    render(withI18n(<MediaRailCard facts={facts} />));
    const row = screen.getByTestId('rail-row-network');
    expect(row.querySelector('img')).toBeNull();
    expect(row.querySelector('span.font-mono')).toBeInTheDocument();
    expect(row).toHaveTextContent('NoLogo Network');
  });

  it('renders keyword chips when keywords prop has items', () => {
    const facts: MediaFact[] = [
      { id: 'status', label: 'Status', value: 'Continuing', testId: 'rail-row-status' },
    ];
    render(withI18n(<MediaRailCard facts={facts} keywords={[{ id: 1, name: 'space race' }]} />));
    expect(screen.getByTestId('rail-keywords')).toBeInTheDocument();
    expect(screen.getByText('space race')).toBeInTheDocument();
  });

  it('hides the keywords block when keywords is empty or absent', () => {
    const facts: MediaFact[] = [
      { id: 'status', label: 'Status', value: 'Continuing', testId: 'rail-row-status' },
    ];
    render(withI18n(<MediaRailCard facts={facts} />));
    expect(screen.queryByTestId('rail-keywords')).toBeNull();
  });

  it('slices keyword chips to 12', () => {
    const facts: MediaFact[] = [
      { id: 'status', label: 'Status', value: 'Continuing', testId: 'rail-row-status' },
    ];
    const keywords = Array.from({ length: 20 }, (_, i) => ({ id: i, name: `kw-${i}` }));
    render(withI18n(<MediaRailCard facts={facts} keywords={keywords} />));
    const chips = screen.getByTestId('rail-keywords').querySelectorAll('span.rounded-md');
    expect(chips).toHaveLength(12);
  });

  it('applies sticky positioning class on desktop', () => {
    const facts: MediaFact[] = [
      { id: 'status', label: 'Status', value: 'Continuing', testId: 'rail-row-status' },
    ];
    render(withI18n(<MediaRailCard facts={facts} />));
    expect(screen.getByTestId('rail-card').className).toMatch(/lg:sticky/);
  });
});
