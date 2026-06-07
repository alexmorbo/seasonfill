import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { renderWithProviders } from '@/test-utils';
import { WatchdogNotConfiguredEmpty } from './WatchdogNotConfiguredEmpty';

function wrap(ui: React.ReactNode) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('<WatchdogNotConfiguredEmpty />', () => {
  it('renders the title, body and three numbered steps', () => {
    renderWithProviders(wrap(<WatchdogNotConfiguredEmpty />));
    expect(screen.getByTestId('watchdog-not-configured')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2 })).toBeInTheDocument();
    // Three step numbers (1, 2, 3) rendered as text content.
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
  });

  it('renders both CTAs as router links to the right routes', () => {
    renderWithProviders(wrap(<WatchdogNotConfiguredEmpty />), {
      route: '/watchdog',
    });
    const links = screen.getAllByRole('link');
    expect(
      links.some((a) => a.getAttribute('href') === '/instances?openCreate=1'),
    ).toBe(true);
    expect(links.some((a) => a.getAttribute('href') === '/instances')).toBe(
      true,
    );
  });
});
