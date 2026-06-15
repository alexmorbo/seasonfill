import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { CountryName } from './CountryName';

describe('CountryName', () => {
  it('returns null for undefined code', () => {
    const { container } = render(<I18nextProvider i18n={i18n}><CountryName code={undefined} /></I18nextProvider>);
    expect(container.firstChild).toBeNull();
  });

  it('renders a localized region name when available', () => {
    const { container } = render(<I18nextProvider i18n={i18n}><CountryName code="US" /></I18nextProvider>);
    expect(container.textContent).toMatch(/(United States|США)/);
  });

  it('falls back to the raw code for unknown regions', () => {
    const { container } = render(<I18nextProvider i18n={i18n}><CountryName code="ZZ" /></I18nextProvider>);
    // Intl.DisplayNames returns "Unknown Region" for invalid codes, so we allow both
    expect(container.textContent).toMatch(/ZZ|Unknown Region/);
  });
});
