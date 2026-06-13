import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { render } from '@testing-library/react';
import i18n from '@/i18n';
import { StatusPill } from './StatusPill';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<StatusPill />', () => {
  it.each(['continuing', 'ended', 'canceled', 'in_production', 'upcoming', 'unknown'] as const)(
    'renders the %s status', (s) => {
      r(<StatusPill status={s} />);
      const pill = screen.getByTestId('status-pill');
      expect(pill.getAttribute('data-status')).toBe(s);
    },
  );

  it('renders an i18n label for continuing', () => {
    r(<StatusPill status="continuing" />);
    expect(screen.getByText('Continuing')).toBeInTheDocument();
  });
});
