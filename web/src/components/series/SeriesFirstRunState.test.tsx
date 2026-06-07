import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { SeriesFirstRunState } from './SeriesFirstRunState';

if (!i18n.isInitialized) {
  void i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  });
}

describe('<SeriesFirstRunState />', () => {
  it('renders first-run testid and the three steps list', () => {
    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <SeriesFirstRunState />
        </MemoryRouter>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('series-first-run')).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(3);
  });
});
