import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { SeriesHeader } from './SeriesHeader';

if (!i18n.isInitialized) {
  void i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  });
}

function wrap(props: Partial<{
  shownCount: number;
  totalCount: number;
  isLoading: boolean;
  isError: boolean;
  onRefresh: () => void;
}> = {}) {
  const onRefresh = props.onRefresh ?? vi.fn();
  render(
    <I18nextProvider i18n={i18n}>
      <SeriesHeader
        shownCount={props.shownCount ?? 12}
        totalCount={props.totalCount ?? 150}
        isLoading={props.isLoading ?? false}
        isError={props.isError ?? false}
        onRefresh={onRefresh}
      />
    </I18nextProvider>,
  );
  return { onRefresh };
}

describe('<SeriesHeader />', () => {
  it('does not render an h1 or h2 heading (topbar owns the title now)', () => {
    wrap({ shownCount: 12, totalCount: 150 });
    expect(screen.queryByRole('heading', { level: 1 })).toBeNull();
    expect(screen.queryByRole('heading', { level: 2 })).toBeNull();
  });

  it('renders the count line with both shown and total values', () => {
    wrap({ shownCount: 12, totalCount: 150 });
    const count = screen.getByTestId('series-header-count');
    expect(count).toBeInTheDocument();
    expect(count.textContent ?? '').toMatch(/12/);
    expect(count.textContent ?? '').toMatch(/150/);
  });

  it('renders the error state with TriangleAlert icon when isError=true', () => {
    wrap({ isError: true });
    const icons = document.querySelectorAll('svg.lucide-triangle-alert');
    expect(icons.length).toBeGreaterThanOrEqual(1);
  });

  it('invokes onRefresh when refresh clicked', () => {
    const { onRefresh } = wrap();
    fireEvent.click(screen.getByTestId('series-header-refresh'));
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('disables refresh button when isLoading=true', () => {
    wrap({ isLoading: true });
    expect(screen.getByTestId('series-header-refresh')).toBeDisabled();
  });
});
