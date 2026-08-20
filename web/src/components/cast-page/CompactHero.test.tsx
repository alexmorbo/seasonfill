import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { CompactHero } from './CompactHero';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<CompactHero />', () => {
  it('renders title, year range and counts', () => {
    r(<CompactHero
      title="For All Mankind"
      posterAsset="aaaa"
      status="continuing"
      yearStart={2019}
      yearEnd={2025}
      castCount={62}
      crewCount={25}
    />);
    expect(screen.getByTestId('cast-page-title')).toHaveTextContent('For All Mankind');
    expect(screen.getByText('2019–2025')).toBeInTheDocument();
    const counts = screen.getByTestId('cast-counts');
    expect(counts).toHaveTextContent('62 cast members');
    expect(counts).toHaveTextContent('25 crew members');
  });

  it('handles missing optional title/poster/year fields', () => {
    r(<CompactHero
      title={undefined}
      posterAsset={undefined}
      status="continuing"
      yearStart={undefined}
      yearEnd={undefined}
      castCount={0}
      crewCount={0}
    />);
    expect(screen.getByTestId('cast-compact-hero')).toBeInTheDocument();
    expect(screen.getByTestId('status-pill')).toHaveAttribute('data-status', 'continuing');
  });

  // Movie usage (no series status vocabulary, no crew list) — `status` and
  // `crewCount` are omitted entirely rather than fed 'unknown'/0, so the
  // status pill and the "· N crew" segment are hidden instead of rendering
  // a misleading "Unknown" / "0 crew".
  it('hides the status pill and crew count when status/crewCount are omitted', () => {
    r(<CompactHero
      title="Dune"
      posterAsset={undefined}
      yearStart={undefined}
      yearEnd={undefined}
      castCount={12}
    />);
    expect(screen.getByTestId('cast-page-title')).toHaveTextContent('Dune');
    expect(screen.queryByTestId('status-pill')).toBeNull();
    const counts = screen.getByTestId('cast-counts');
    expect(counts).toHaveTextContent('12 cast members');
    expect(counts.textContent).not.toContain('crew');
  });
});
