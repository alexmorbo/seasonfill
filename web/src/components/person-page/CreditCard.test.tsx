import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { CreditCard } from './CreditCard';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

describe('<CreditCard />', () => {
  it('renders an internal Link when link.kind === "internal"', () => {
    r(
      <CreditCard
        testId="x"
        title="Show"
        link={{ kind: 'internal', to: '/series/777' }}
      />,
    );
    const card = screen.getByTestId('x');
    expect(card.tagName.toLowerCase()).toBe('a');
    expect(card.getAttribute('href')).toBe('/series/777');
    expect(card.getAttribute('target')).toBeNull();
  });

  it('renders an external anchor when link.kind === "tmdb"', () => {
    r(
      <CreditCard
        testId="x"
        title="Show"
        link={{ kind: 'tmdb', href: 'https://www.themoviedb.org/tv/1' }}
      />,
    );
    const card = screen.getByTestId('x');
    expect(card.tagName.toLowerCase()).toBe('a');
    expect(card.getAttribute('href')).toBe('https://www.themoviedb.org/tv/1');
    expect(card.getAttribute('target')).toBe('_blank');
    expect(card.getAttribute('rel')).toContain('noreferrer');
  });

  it('renders a non-clickable div when link.kind === "none"', () => {
    r(<CreditCard testId="x" title="Show" link={{ kind: 'none' }} />);
    const card = screen.getByTestId('x');
    expect(card.tagName.toLowerCase()).toBe('div');
    expect(card.getAttribute('href')).toBeNull();
  });

  it('renders title + year + role + footer when all present', () => {
    r(
      <CreditCard
        testId="x"
        title="Show"
        year={2024}
        role="Lead role"
        footer={<div data-testid="x-footer">homelab</div>}
        link={{ kind: 'none' }}
      />,
    );
    expect(screen.getByText('Show · 2024')).toBeInTheDocument();
    expect(screen.getByText('Lead role')).toBeInTheDocument();
    expect(screen.getByTestId('x-footer')).toHaveTextContent('homelab');
  });

  it('forwards dataAttrs as data-* attributes on the root', () => {
    r(
      <CreditCard
        testId="x"
        title="Show"
        link={{ kind: 'internal', to: '/series/1' }}
        dataAttrs={{ 'series-id': 777, instance: 'homelab' }}
      />,
    );
    const card = screen.getByTestId('x');
    expect(card.getAttribute('data-series-id')).toBe('777');
    expect(card.getAttribute('data-instance')).toBe('homelab');
  });
});
