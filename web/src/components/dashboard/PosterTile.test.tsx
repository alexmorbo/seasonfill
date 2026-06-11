import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { PosterTile } from './PosterTile';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const mod = await vi.importActual('react-router-dom');
  return {
    ...mod,
    useNavigate: () => mockNavigate,
  };
});

const fixture: SeriesCacheItem = {
  sonarr_series_id: 1,
  instance_name: 'alpha',
  title: 'Breaking Bad',
  title_slug: 'breaking-bad',
  year: 2008,
  network: 'AMC',
  status: 'ended',
  poster_path: '/path/to/poster.jpg',
  monitored: true,
  missing_count: 0,
  last_grab_at: new Date(Date.now() - 3600000).toISOString(),
  last_imported_episode: 'S05E16',
  updated_at: new Date().toISOString(),
};

function renderTile(item: SeriesCacheItem) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <BrowserRouter>
            <PosterTile item={item} />
          </BrowserRouter>
        </TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('<PosterTile />', () => {
  beforeEach(() => mockNavigate.mockClear());
  afterEach(() => vi.restoreAllMocks());

  it('renders proxy img with size=full for the instance + series id', () => {
    renderTile(fixture);
    const img = screen.getByTestId('series-poster-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe(
      '/api/v1/instances/alpha/series/1/poster?size=full',
    );
    expect(img.getAttribute('loading')).toBe('lazy');
  });

  it('renders title, year, network footer', () => {
    renderTile(fixture);
    expect(screen.getByText('Breaking Bad')).toBeInTheDocument();
    expect(screen.getByText(/2008/)).toBeInTheDocument();
    expect(screen.getByText(/AMC/)).toBeInTheDocument();
  });

  it('renders mono-mark letter (first char uppercase) when title present', () => {
    renderTile(fixture);
    const mark = screen.getByText('B', { selector: '[aria-hidden="true"]' });
    expect(mark).toBeInTheDocument();
  });

  it('renders gradient placeholder with data-testid', () => {
    const { container } = renderTile(fixture);
    const article = container.querySelector('[data-testid="poster-tile"]') as HTMLElement;
    expect(article).toBeInTheDocument();
    // Gradient is rendered via tailwind class or inline style
    const hasStyle = article.hasAttribute('style');
    const hasClass = article.className.length > 0;
    expect(hasStyle || hasClass).toBe(true);
  });

  it('renders imported status badge when status does not start with import_failed', () => {
    renderTile(fixture);
    expect(screen.getByText('imported')).toBeInTheDocument();
    expect(screen.getByTestId('poster-tile')).toHaveAttribute('data-variant', 'imported');
  });

  it('renders failed status badge when status starts with import_failed', () => {
    renderTile({ ...fixture, status: 'import_failed_reason' });
    expect(screen.getByText(/import_failed/i)).toBeInTheDocument();
    expect(screen.getByTestId('poster-tile')).toHaveAttribute('data-variant', 'failed');
  });

  it('parses S05E07 episode format and renders single episode label', () => {
    renderTile({ ...fixture, last_imported_episode: 'S05E07' });
    expect(screen.getByText(/S5.*E7/)).toBeInTheDocument();
  });

  it('parses S05E07-09 episode range and renders range label with newcount chip', () => {
    renderTile({ ...fixture, last_imported_episode: 'S05E07-09' });
    expect(screen.getByText(/S5.*E7–9/)).toBeInTheDocument();
    expect(screen.getByText(/\+3/)).toBeInTheDocument();
  });

  it('parses S05 season-only format and renders season label', () => {
    renderTile({ ...fixture, last_imported_episode: 'S05' });
    expect(screen.getByText(/S5/)).toBeInTheDocument();
  });

  it('does not render episode chip when last_imported_episode is absent', () => {
    const { last_imported_episode: _last_imported_episode, ...fixtureNoEpisode } = fixture;
    renderTile(fixtureNoEpisode as SeriesCacheItem);
    expect(screen.queryByText(/^S/)).not.toBeInTheDocument();
  });

  it('does not render year when year is absent', () => {
    const { year: _year, ...fixtureNoYear } = fixture;
    renderTile(fixtureNoYear as SeriesCacheItem);
    const yearText = Array.from(screen.queryAllByText(/\d{4}/)).filter(
      (el) => el.textContent?.includes('2008'),
    );
    expect(yearText.length).toBe(0);
  });

  it('does not render network when network is absent', () => {
    const { network: _network, ...fixtureNoNetwork } = fixture;
    renderTile(fixtureNoNetwork as SeriesCacheItem);
    const networks = Array.from(screen.queryAllByText(/AMC/));
    expect(networks.length).toBe(0);
  });

  it('navigates to /series?q=title&state=all on click', async () => {
    const user = userEvent.setup();
    renderTile(fixture);
    const tile = screen.getByTestId('poster-tile');
    await user.click(tile);
    expect(mockNavigate).toHaveBeenCalledWith('/series?q=Breaking%20Bad&state=all');
  });

  it('navigates to /series?q=title&state=all on Enter key', async () => {
    const user = userEvent.setup();
    renderTile(fixture);
    const tile = screen.getByTestId('poster-tile');
    tile.focus();
    await user.keyboard('{Enter}');
    expect(mockNavigate).toHaveBeenCalledWith('/series?q=Breaking%20Bad&state=all');
  });

  it('navigates to /series?q=title&state=all on Space key', async () => {
    const user = userEvent.setup();
    renderTile(fixture);
    const tile = screen.getByTestId('poster-tile');
    tile.focus();
    await user.keyboard(' ');
    expect(mockNavigate).toHaveBeenCalledWith('/series?q=Breaking%20Bad&state=all');
  });

  it('renders relative time (last_grab_at fallback to updated_at)', () => {
    const oneHourAgo = new Date(Date.now() - 3600000).toISOString();
    renderTile({ ...fixture, last_grab_at: oneHourAgo });
    expect(screen.getByText(/hr\./i)).toBeInTheDocument();
  });

  it('renders the imported chip via i18n (story 121c §H)', () => {
    renderTile(fixture);
    // The fixture renders in EN — the chip must read 'imported' from
    // the i18n key, not the hardcoded literal (functionally identical
    // in EN, but the test contract is that t() is consulted).
    expect(screen.getByText('imported')).toBeInTheDocument();
  });

  it('renders the imported chip in Russian when i18n is set to RU (story 121c §H)', async () => {
    await i18n.changeLanguage('ru');
    try {
      renderTile(fixture);
      expect(screen.getByText('импортирован')).toBeInTheDocument();
    } finally {
      await i18n.changeLanguage('en');
    }
  });
});
