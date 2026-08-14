import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { MovieOverviewBlock } from './MovieOverviewBlock';

function wrap(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('MovieOverviewBlock', () => {
  it('renders the title, tagline and overview text', () => {
    render(wrap(
      <MovieOverviewBlock
        tmdbId={603}
        title="The Matrix"
        tagline="Welcome to the Real World."
        overview="A computer hacker learns the true nature of reality."
      />,
    ));
    expect(screen.getByTestId('movie-overview-block')).toBeTruthy();
    expect(screen.getByTestId('movie-overview-title').textContent).toBe('The Matrix');
    expect(screen.getByTestId('movie-overview-tagline').textContent).toBe(
      'Welcome to the Real World.',
    );
    expect(screen.getByTestId('movie-overview-text').textContent).toBe(
      'A computer hacker learns the true nature of reality.',
    );
  });

  it('renders the skeleton block when loading', () => {
    render(wrap(<MovieOverviewBlock tmdbId={603} loading />));
    expect(screen.getByTestId('movie-overview-block-loading')).toBeTruthy();
    expect(screen.getByTestId('movie-overview-skeleton')).toBeTruthy();
    // No content section while loading.
    expect(screen.queryByTestId('movie-overview-block')).toBeNull();
    expect(screen.queryByTestId('movie-overview-text')).toBeNull();
  });

  it('shows the empty placeholder when overview is absent', () => {
    render(wrap(<MovieOverviewBlock tmdbId={603} title="The Matrix" />));
    expect(screen.getByTestId('movie-overview-empty')).toBeTruthy();
    expect(screen.queryByTestId('movie-overview-text')).toBeNull();
  });

  it('shows the empty placeholder when overview is whitespace-only', () => {
    render(wrap(<MovieOverviewBlock tmdbId={603} overview="   " />));
    expect(screen.getByTestId('movie-overview-empty')).toBeTruthy();
    expect(screen.queryByTestId('movie-overview-text')).toBeNull();
  });

  it('omits the tagline when none is provided', () => {
    render(wrap(<MovieOverviewBlock tmdbId={603} title="The Matrix" overview="Body." />));
    expect(screen.queryByTestId('movie-overview-tagline')).toBeNull();
  });

  it('omits the title element when no title is provided', () => {
    render(wrap(<MovieOverviewBlock tmdbId={603} overview="Body." />));
    expect(screen.queryByTestId('movie-overview-title')).toBeNull();
  });

  it('shows the language-fallback signal when served_language differs from requested', () => {
    render(wrap(
      <MovieOverviewBlock
        tmdbId={603}
        title="The Matrix"
        overview="Body."
        servedLanguage="en-US"
        requestedLang="ru-RU"
      />,
    ));
    const tag = screen.getByTestId('movie-overview-lang-fallback');
    expect(tag.getAttribute('data-content-lang')).toBe('en');
  });

  it('hides the language-fallback signal when served_language matches requested', () => {
    render(wrap(
      <MovieOverviewBlock
        tmdbId={603}
        title="Матрица"
        overview="Тело."
        servedLanguage="ru-RU"
        requestedLang="ru-RU"
      />,
    ));
    expect(screen.queryByTestId('movie-overview-lang-fallback')).toBeNull();
  });

  it('hides the language-fallback signal when served_language is absent', () => {
    render(wrap(
      <MovieOverviewBlock tmdbId={603} title="The Matrix" overview="Body." requestedLang="ru-RU" />,
    ));
    expect(screen.queryByTestId('movie-overview-lang-fallback')).toBeNull();
  });
});
