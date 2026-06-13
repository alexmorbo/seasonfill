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

  it('handles missing optional fields', () => {
    r(<CompactHero
      title={undefined}
      posterAsset={undefined}
      status={undefined}
      yearStart={undefined}
      yearEnd={undefined}
      castCount={0}
      crewCount={0}
    />);
    expect(screen.getByTestId('cast-compact-hero')).toBeInTheDocument();
    expect(screen.getByTestId('status-pill')).toHaveAttribute('data-status', 'unknown');
  });
});
