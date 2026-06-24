import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { renderWithProviders } from '@/test-utils';
import { InLibraryBadge } from './InLibraryBadge';

const renderBadge = (instances: readonly string[]) =>
  renderWithProviders(
    <I18nextProvider i18n={i18n}>
      <InLibraryBadge instances={instances} />
    </I18nextProvider>,
  );

describe('<InLibraryBadge />', () => {
  it('renders nothing when instances is empty', () => {
    renderBadge([]);
    expect(screen.queryByTestId('discovery-in-library-badge')).toBeNull();
  });

  it('renders translated badge label when non-empty', () => {
    renderBadge(['sonarr-alpha', 'sonarr-4k']);
    const badge = screen.getByTestId('discovery-in-library-badge');
    expect(badge.textContent ?? '').toMatch(/library|библиотеке/i);
  });
});
