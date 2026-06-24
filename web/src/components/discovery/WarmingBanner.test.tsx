import type { ReactElement } from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WarmingBanner } from './WarmingBanner';

function r(ui: ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{ui}</I18nextProvider>);
}

describe('<WarmingBanner />', () => {
  it('renders cold-start text with interpolated seconds', () => {
    r(<WarmingBanner kind="cold_start" estimateSeconds={42} />);
    const node = screen.getByTestId('discovery-warming-banner');
    expect(node).toHaveAttribute('aria-live', 'polite');
    expect(node).toHaveAttribute('data-kind', 'cold_start');
    expect(node.textContent).toMatch(/42/);
  });

  it('falls back to 30s when estimate is missing', () => {
    r(<WarmingBanner kind="cold_start" />);
    expect(screen.getByTestId('discovery-warming-banner').textContent).toMatch(/30/);
  });

  it('renders the TMDB throttle text', () => {
    r(<WarmingBanner kind="tmdb_throttled" retryAfterSeconds={4} />);
    const node = screen.getByTestId('discovery-warming-banner');
    expect(node).toHaveAttribute('data-kind', 'tmdb_throttled');
    expect(node).toHaveAttribute('data-retry-after', '4');
    expect(node.textContent).toMatch(/TMDB/);
  });
});
