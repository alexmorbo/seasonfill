import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { ETAChip } from '../ETAChip';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<ETAChip />', () => {
  it('maps qBit infinity sentinel to ∞', () => {
    r(<ETAChip seconds={8640000} />);
    expect(screen.getByTestId('eta-chip').textContent).toBe('∞');
  });

  it('formats minutes', () => {
    r(<ETAChip seconds={720} />);
    expect(screen.getByTestId('eta-chip').textContent).toBe('12m');
  });

  it('formats hours+minutes', () => {
    r(<ETAChip seconds={3 * 3600 + 20 * 60} />);
    expect(screen.getByTestId('eta-chip').textContent).toBe('3h 20m');
  });

  it('shows em-dash when muted', () => {
    r(<ETAChip seconds={720} muted />);
    expect(screen.getByTestId('eta-chip').textContent).toBe('—');
  });

  it('shows em-dash on zero', () => {
    r(<ETAChip seconds={0} />);
    expect(screen.getByTestId('eta-chip').textContent).toBe('—');
  });
});
