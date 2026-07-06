import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';

import { SeriesCardTile } from './SeriesCardTile';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

// dashboard fixture: Breaking Bad, no series_id (legacy row), episode present.
const dashboardFixture: SeriesCacheItem = {
  sonarr_series_id: 1,
  instance_name: 'alpha',
  title: 'Breaking Bad',
  title_slug: 'breaking-bad',
  year: 2008,
  network: 'AMC',
  status: 'ended',
  poster_hash: 'cafebabe',
  monitored: true,
  missing_count: 0,
  last_grab_at: new Date(Date.now() - 3600000).toISOString(),
  last_imported_episode: 'S05E16',
  updated_at: new Date().toISOString(),
};

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="probe-location">{loc.pathname + loc.search}</div>;
}

function renderTile(item: SeriesCacheItem) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter initialEntries={['/']}>
            <Routes>
              <Route
                path="/"
                element={<SeriesCardTile item={item} />}
              />
              <Route path="/series/:id" element={<LocationProbe />} />
              <Route path="/series/:instance/:id" element={<LocationProbe />} />
            </Routes>
          </MemoryRouter>
        </TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('<SeriesCardTile /> (dashboard poster tile)', () => {
  it('renders content-addressed media img for poster_hash', () => {
    renderTile(dashboardFixture);
    const img = screen.getByTestId('media-image-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/v1/media/cafebabe');
    expect(img.getAttribute('loading')).toBe('lazy');
  });

  it('renders monogram fallback when poster_hash is absent', () => {
    const { poster_hash: _ph, ...rest } = dashboardFixture;
    renderTile(rest as SeriesCacheItem);
    expect(screen.queryByTestId('media-image-img')).toBeNull();
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });

  it('Story 494 / B-16b: renders MonogramFallback when poster_hash is omitted (TMDB-disabled fallback)', () => {
    const { poster_hash: _ph, ...rest } = dashboardFixture;
    renderTile(rest as SeriesCacheItem);
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
    expect(screen.queryByTestId('media-image-img')).toBeNull();
  });

  it("renders brand 'sf' monogram glyph when poster_hash absent", () => {
    const { poster_hash: _ph, ...rest } = dashboardFixture;
    renderTile(rest as SeriesCacheItem);
    const fallback = screen.getByTestId('monogram-fallback');
    const glyph = fallback.querySelector('span.glyph') as HTMLSpanElement;
    expect(glyph).not.toBeNull();
    expect(glyph.textContent).toBe('sf');
  });

  it('does not emit legacy /api/v1/instances/.../poster URL', () => {
    renderTile(dashboardFixture);
    const imgs = document.querySelectorAll('img');
    imgs.forEach((img) => {
      expect(img.getAttribute('src') ?? '').not.toMatch(
        /\/api\/v1\/instances\/[^/]+\/series\/\d+\/poster/,
      );
    });
  });

  it('renders title, year, network footer', () => {
    renderTile(dashboardFixture);
    expect(screen.getByText('Breaking Bad')).toBeInTheDocument();
    expect(screen.getByText(/2008/)).toBeInTheDocument();
    expect(screen.getByText(/AMC/)).toBeInTheDocument();
  });

  it('renders gradient placeholder with data-testid', () => {
    const { container } = renderTile(dashboardFixture);
    const article = container.querySelector(
      '[data-testid="poster-tile"]',
    ) as HTMLElement;
    expect(article).toBeInTheDocument();
    const hasStyle = article.hasAttribute('style');
    const hasClass = article.className.length > 0;
    expect(hasStyle || hasClass).toBe(true);
  });

  it('renders imported status badge when status does not start with import_failed', () => {
    renderTile(dashboardFixture);
    expect(screen.getByText('imported')).toBeInTheDocument();
    expect(screen.getByTestId('poster-tile')).toHaveAttribute(
      'data-variant',
      'imported',
    );
  });

  it('renders failed status badge when status starts with import_failed', () => {
    renderTile({ ...dashboardFixture, status: 'import_failed_reason' });
    expect(screen.getByText(/import_failed/i)).toBeInTheDocument();
    expect(screen.getByTestId('poster-tile')).toHaveAttribute(
      'data-variant',
      'failed',
    );
  });

  it('parses S05E07 episode format and renders single episode label', () => {
    renderTile({ ...dashboardFixture, last_imported_episode: 'S05E07' });
    expect(screen.getByText(/S5.*E7/)).toBeInTheDocument();
  });

  it('parses S05E07-09 episode range and renders range label with newcount chip', () => {
    renderTile({ ...dashboardFixture, last_imported_episode: 'S05E07-09' });
    expect(screen.getByText(/S5.*E7–9/)).toBeInTheDocument();
    expect(screen.getByText(/\+3/)).toBeInTheDocument();
  });

  it('parses S05 season-only format and renders season label', () => {
    renderTile({ ...dashboardFixture, last_imported_episode: 'S05' });
    expect(screen.getByText(/S5/)).toBeInTheDocument();
  });

  it('does not render episode chip when last_imported_episode is absent', () => {
    const { last_imported_episode: _last, ...rest } = dashboardFixture;
    renderTile(rest as SeriesCacheItem);
    expect(screen.queryByText(/^S/)).not.toBeInTheDocument();
  });

  it('does not render year when year is absent', () => {
    const { year: _year, ...rest } = dashboardFixture;
    renderTile(rest as SeriesCacheItem);
    const yearText = Array.from(screen.queryAllByText(/\d{4}/)).filter((el) =>
      el.textContent?.includes('2008'),
    );
    expect(yearText.length).toBe(0);
  });

  it('does not render network when network is absent', () => {
    const { network: _network, ...rest } = dashboardFixture;
    renderTile(rest as SeriesCacheItem);
    const networks = Array.from(screen.queryAllByText(/AMC/));
    expect(networks.length).toBe(0);
  });

  it('renders relative time (last_grab_at fallback to updated_at)', () => {
    const oneHourAgo = new Date(Date.now() - 3600000).toISOString();
    renderTile({ ...dashboardFixture, last_grab_at: oneHourAgo });
    expect(screen.getByText(/hr\./i)).toBeInTheDocument();
  });

  it('renders the imported chip via i18n (story 121c §H)', () => {
    renderTile(dashboardFixture);
    expect(screen.getByText('imported')).toBeInTheDocument();
  });

  it('renders the imported chip in Russian when i18n is set to RU (story 121c §H)', async () => {
    await i18n.changeLanguage('ru');
    try {
      renderTile(dashboardFixture);
      expect(screen.getByText('импортирован')).toBeInTheDocument();
    } finally {
      await i18n.changeLanguage('en');
    }
  });
});

describe('<SeriesCardTile /> navigation (canonical-first, wrong-id bug fix)', () => {
  it('item with canonical series_id=42 navigates to /series/42 on click', () => {
    renderTile({ ...dashboardFixture, series_id: 42, sonarr_series_id: 1 });
    fireEvent.click(screen.getByTestId('poster-tile'));
    expect(screen.getByTestId('probe-location').textContent).toBe('/series/42');
  });

  it('item with canonical series_id navigates on Enter keypress to /series/42', () => {
    renderTile({ ...dashboardFixture, series_id: 42, sonarr_series_id: 1 });
    fireEvent.keyDown(screen.getByTestId('poster-tile'), { key: 'Enter' });
    expect(screen.getByTestId('probe-location').textContent).toBe('/series/42');
  });

  it('item WITHOUT series_id falls back to legacy /series/alpha/1', () => {
    renderTile(dashboardFixture);
    fireEvent.click(screen.getByTestId('poster-tile'));
    expect(screen.getByTestId('probe-location').textContent).toBe(
      '/series/alpha/1',
    );
  });

  // THE BUG-FIX PROOF (Story 961): an item carrying BOTH a canonical series_id
  // AND an instance-scoped sonarr_series_id must navigate to the canonical
  // /series/:id, NOT the legacy 3-segment route. This is the direct regression
  // test for "The Bear → Sense & Sensibility".
  it('item WITH canonical series_id navigates to /series/900, NOT legacy /series/alpha/1', () => {
    renderTile({ ...dashboardFixture, series_id: 900, sonarr_series_id: 1 });
    fireEvent.click(screen.getByTestId('poster-tile'));
    expect(screen.getByTestId('probe-location').textContent).toBe(
      '/series/900',
    );
  });
});
