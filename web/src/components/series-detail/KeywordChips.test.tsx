import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { KeywordChips } from './KeywordChips';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<KeywordChips />', () => {
  it('renders chips when provided', () => {
    r(<KeywordChips chips={[
      { id: 1, name: 'time travel' },
      { id: 2, name: 'dystopia' },
    ]} />);
    expect(screen.getByTestId('keyword-chips')).toBeInTheDocument();
    expect(screen.getAllByTestId('keyword-chip')).toHaveLength(2);
    expect(screen.getByText('time travel')).toBeInTheDocument();
  });

  it('returns null when chips is empty', () => {
    const { container } = r(<KeywordChips chips={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null when chips is undefined', () => {
    const { container } = r(<KeywordChips />);
    expect(container.firstChild).toBeNull();
  });

  it('respects the limit prop', () => {
    const many = Array.from({ length: 20 }, (_, i) => ({ id: i, name: `kw-${i}` }));
    r(<KeywordChips chips={many} limit={5} />);
    expect(screen.getAllByTestId('keyword-chip')).toHaveLength(5);
  });
});
