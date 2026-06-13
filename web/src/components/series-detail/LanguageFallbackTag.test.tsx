import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { LanguageFallbackTag } from './LanguageFallbackTag';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>{node}</I18nextProvider>,
  );
}

describe('<LanguageFallbackTag />', () => {
  it('renders "en" when content is en-US and requested is ru-RU', () => {
    r(<LanguageFallbackTag contentLang="en-US" requestedLang="ru-RU" />);
    const tag = screen.getByTestId('language-fallback-tag');
    expect(tag.textContent).toBe('en');
    expect(tag.getAttribute('data-content-lang')).toBe('en');
  });

  it('returns null when families match (en vs en-US)', () => {
    const { container } = r(
      <LanguageFallbackTag contentLang="en-US" requestedLang="en" />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('returns null when contentLang is empty', () => {
    const { container } = r(
      <LanguageFallbackTag contentLang={undefined} requestedLang="ru" />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('honours the optional testid override', () => {
    r(
      <LanguageFallbackTag
        contentLang="en"
        requestedLang="ru"
        testid="overview-lang-fallback"
      />,
    );
    expect(screen.getByTestId('overview-lang-fallback')).toBeInTheDocument();
  });
});
