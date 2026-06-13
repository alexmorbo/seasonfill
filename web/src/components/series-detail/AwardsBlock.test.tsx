import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { AwardsBlock } from './AwardsBlock';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider delayDuration={0}>{node}</TooltipProvider>
    </I18nextProvider>,
  );
}

describe('<AwardsBlock />', () => {
  it('renders the awards string when present', () => {
    r(<AwardsBlock awards="Won 16 Primetime Emmys" />);
    expect(screen.getByTestId('awards-block')).toBeInTheDocument();
    expect(screen.getByTestId('awards-text')).toHaveTextContent('Won 16 Primetime Emmys');
  });

  it('returns null when awards is undefined', () => {
    const { container } = r(<AwardsBlock awards={undefined} />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null when awards is an empty string', () => {
    const { container } = r(<AwardsBlock awards="" />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null when awards is the literal "N/A"', () => {
    const { container } = r(<AwardsBlock awards="N/A" />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null when awards is "n/a" (case-insensitive)', () => {
    const { container } = r(<AwardsBlock awards=" n/a " />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null when omdbDegraded is true even with valid awards', () => {
    const { container } = r(
      <AwardsBlock awards="Won 16 Primetime Emmys" omdbDegraded syncedAt="2026-06-13T00:00:00Z" />,
    );
    expect(container.firstChild).toBeNull();
  });
});
