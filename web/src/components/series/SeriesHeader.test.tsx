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
  it('renders the title and count', () => {
    wrap({ shownCount: 12, totalCount: 150 });
    expect(screen.getByRole('heading')).toBeInTheDocument();
    // i18n fallback emits the raw key; whatever the renderer produces,
    // both numbers must appear in the document.
    expect(screen.getByText(/12/)).toBeInTheDocument();
    expect(screen.getByText(/150/)).toBeInTheDocument();
  });

  it('renders the error state when isError=true', () => {
    wrap({ isError: true });
    // The error label is the i18n key when no translation is bound; we
    // assert via testid presence of the warn icon's lucide class.
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
