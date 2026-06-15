import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { NextEpisodeCard } from './NextEpisodeCard';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

const futureISO = new Date(Date.now() + 4 * 86_400_000).toISOString();

describe('NextEpisodeCard', () => {
  it('renders the counter variant for a continuing series with a date', () => {
    render(withI18n(<NextEpisodeCard
      status="continuing"
      nextEpisode={{ season_number: 5, episode_number: 3, title: 'Glasnost', air_date: futureISO }}
    />));
    const el = screen.getByTestId('next-episode-card');
    expect(el.dataset['variant']).toBe('default');
    expect(screen.getByTestId('ip-cd-badge').dataset['variant']).toBe('counter');
    expect(el.textContent).toMatch(/S05E03/);
    expect(el.textContent).toMatch(/Glasnost/);
  });

  it('renders the ended variant with a flag badge', () => {
    render(withI18n(<NextEpisodeCard status="ended" yearEnd={2024} />));
    const el = screen.getByTestId('next-episode-card');
    expect(el.dataset['variant']).toBe('ended');
    expect(screen.getByTestId('ip-cd-badge').dataset['variant']).toBe('muted');
    expect(el.textContent).toMatch(/2024/);
  });

  it('renders the production variant with a hammer badge', () => {
    render(withI18n(<NextEpisodeCard status="in_production" />));
    const el = screen.getByTestId('next-episode-card');
    expect(el.dataset['variant']).toBe('production');
    expect(screen.getByTestId('ip-cd-badge').dataset['variant']).toBe('muted');
  });

  it('returns null for continuing without a scheduled next episode', () => {
    const { container } = render(withI18n(<NextEpisodeCard status="continuing" />));
    expect(container.firstChild).toBeNull();
  });

  it('panel variant uses bordered surface (no glass blur)', () => {
    render(withI18n(<NextEpisodeCard
      status="ended" yearEnd={2024} variant="panel"
    />));
    const el = screen.getByTestId('next-episode-card');
    expect(el.className).toMatch(/border-border-faint/);
    expect(el.className).not.toMatch(/backdrop-filter:blur/);
  });
});
