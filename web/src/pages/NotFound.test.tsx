import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { NotFound } from './NotFound';

describe('<NotFound />', () => {
  it('renders the 404 heading and message (B-47)', () => {
    renderWithProviders(<NotFound />, { route: '/junk-page' });
    expect(
      screen.getByRole('heading', { name: /page not found/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/does not exist or was moved/i),
    ).toBeInTheDocument();
  });

  it('surfaces the requested path so the broken URL stays visible', () => {
    renderWithProviders(<NotFound />, { route: '/people/87819' });
    expect(screen.getByTestId('notfound-path')).toHaveTextContent(
      '/people/87819',
    );
  });

  it('renders a back-home link pointing at "/"', () => {
    renderWithProviders(<NotFound />, { route: '/junk' });
    const link = screen.getByTestId('notfound-home-link');
    expect(link).toHaveAttribute('href', '/');
    expect(link).toHaveTextContent(/back to dashboard/i);
  });

  it('renders the stage chrome (testid hook for Playwright)', () => {
    renderWithProviders(<NotFound />, { route: '/foo' });
    expect(screen.getByTestId('notfound-stage')).toBeInTheDocument();
  });
});
