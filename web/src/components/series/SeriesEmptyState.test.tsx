import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { SeriesEmptyState } from './SeriesEmptyState';

if (!i18n.isInitialized) {
  void i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  });
}

describe('<SeriesEmptyState />', () => {
  it('renders the server variant', () => {
    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <SeriesEmptyState variant="server" />
        </MemoryRouter>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('series-empty-server')).toBeInTheDocument();
  });

  it('renders the filtered variant with clear CTA', () => {
    const onClear = vi.fn();
    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <SeriesEmptyState variant="filtered" onClearFilters={onClear} />
        </MemoryRouter>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('series-empty-filtered')).toBeInTheDocument();
    const btn = screen.getByRole('button');
    fireEvent.click(btn);
    expect(onClear).toHaveBeenCalledTimes(1);
  });
});
