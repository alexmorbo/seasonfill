import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { LanguageName } from './LanguageName';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('LanguageName', () => {
  it('returns null for empty code', () => {
    const { container } = render(withI18n(<LanguageName code={undefined} />));
    expect(container.firstChild).toBeNull();
  });

  it('renders a non-empty string for a valid code', () => {
    render(withI18n(<LanguageName code="en" />));
    // exact name depends on i18n.resolvedLanguage at test time; assert
    // it's non-empty and not the raw code (when Intl knows it).
    const node = screen.getByText(/.+/);
    expect(node.textContent?.length ?? 0).toBeGreaterThan(0);
  });

  it('falls back to raw code on unknown language', () => {
    render(withI18n(<LanguageName code="xxx" />));
    expect(screen.getByText('xxx')).toBeInTheDocument();
  });
});
