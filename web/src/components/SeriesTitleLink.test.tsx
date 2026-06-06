import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SeriesTitleLink } from './SeriesTitleLink';

describe('<SeriesTitleLink />', () => {
  it('renders an external link when title_slug and ui_url are present', () => {
    render(
      <SeriesTitleLink
        title="Severance"
        titleSlug="severance"
        year={2022}
        instanceUiUrl="https://sonarr.example.com"
      />,
    );
    const link = screen.getByRole('link', { name: /Severance/i });
    expect(link).toHaveAttribute(
      'href',
      'https://sonarr.example.com/series/severance',
    );
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(screen.getByText('(2022)')).toBeInTheDocument();
  });

  it('strips trailing slashes on the instance URL before joining', () => {
    render(
      <SeriesTitleLink
        title="Andor"
        titleSlug="andor"
        instanceUiUrl="https://sonarr.example.com/"
      />,
    );
    expect(screen.getByRole('link', { name: /Andor/i })).toHaveAttribute(
      'href',
      'https://sonarr.example.com/series/andor',
    );
  });

  it('falls back to plain text when title_slug is missing', () => {
    render(
      <SeriesTitleLink
        title="Severance"
        instanceUiUrl="https://sonarr.example.com"
      />,
    );
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('Severance')).toBeInTheDocument();
  });

  it('falls back to plain text when instance ui_url is missing', () => {
    render(<SeriesTitleLink title="Severance" titleSlug="severance" />);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('Severance')).toBeInTheDocument();
  });

  it('omits the year suffix when year is absent', () => {
    render(
      <SeriesTitleLink
        title="Severance"
        titleSlug="severance"
        instanceUiUrl="https://sonarr.example.com"
      />,
    );
    expect(screen.queryByText(/\(\d{4}\)/)).not.toBeInTheDocument();
  });

  it('renders the year suffix even in the plain-text fallback', () => {
    render(<SeriesTitleLink title="Severance" year={2022} />);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('(2022)')).toBeInTheDocument();
  });
});
