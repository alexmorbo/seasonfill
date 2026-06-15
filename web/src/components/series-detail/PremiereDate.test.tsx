import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { PremiereDate } from './PremiereDate';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('PremiereDate', () => {
  it('returns null for undefined', () => {
    const { container } = render(withI18n(<PremiereDate iso={undefined} />));
    expect(container.firstChild).toBeNull();
  });

  it('passes through malformed input', () => {
    render(withI18n(<PremiereDate iso="not-a-date" />));
    expect(screen.getByText('not-a-date')).toBeInTheDocument();
  });

  it('formats a valid ISO date into a non-empty localized string', () => {
    render(withI18n(<PremiereDate iso="2026-05-28" />));
    // Year should always appear in the localized output.
    expect(screen.getByText(/2026/)).toBeInTheDocument();
  });

  it('does not drift across timezones — May 28 stays May 28', () => {
    // Sanity: the regex parse + local-Date construction prevents UTC
    // midnight from rendering as "May 27" in negative offsets.
    render(withI18n(<PremiereDate iso="2026-05-28" />));
    // Day component should be 28 regardless of test machine TZ.
    expect(screen.getByText(/28/)).toBeInTheDocument();
  });
});
