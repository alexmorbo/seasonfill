import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import i18n from '@/i18n';
import { MovieDetail } from './MovieDetail';
import { AddToRadarrProvider } from '@/components/movies/AddToRadarrProvider';

const origFetch = globalThis.fetch;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function spyFetch(body: unknown, status = 200) {
  const fn = vi.fn(async (input: string | URL | Request) => {
    const url = typeof input === 'string' ? input : input.toString();
    // B1.5/ADR-0023 — MovieTorrentsSection now mounts unconditionally;
    // without this branch every spyFetch-based test would feed the movie
    // payload to useQbitSettings as if it were a qBit settings response.
    // 404 keeps the panel a no-op (`useQbitSettings` is 404-tolerant),
    // restoring this helper's pre-B1.5 behavior for tests that don't
    // care about torrents.
    if (url.includes('/qbit/settings')) return json({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404);
    return json(body, status);
  });
  globalThis.fetch = fn as typeof fetch;
}

function movie() {
  return {
    tmdb_id: 438631,
    title: 'Dune',
    year: 2021,
    tagline: 'Beyond fear, destiny awaits.',
    status: 'Released',
    runtime_minutes: 155,
    release_date: '2021-10-22T00:00:00Z',
    overview: 'Paul Atreides arrives on Arrakis.',
    tmdb_rating: 8.1,
    imdb_rating: 8.0,
    imdb_id: 'tt1160419',
    studio: 'Legendary Pictures',
    country: 'US',
    countries: ['US'],
    original_language: 'en',
    genres: [{ id: 878, name: 'Science Fiction', language: 'en-US' }],
    keywords: [{ id: 1, name: 'desert', language: 'en-US' }],
    trailer: { key: 'n9xhJrPXop4', name: 'Official Trailer', site: 'YouTube' },
    library: [
      { instance_name: 'radarr', monitored: true, has_file: true, availability: 'released' },
    ],
  };
}

// Routes each per-section sub-fetch to a distinct payload so the composed page
// renders every section. Falls back to the base movie for /movies/:id and any
// unmatched URL. Keeps the page a unit test (network fully stubbed).
function routedFetch(base: Record<string, unknown> = movie()) {
  globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
    const url = typeof input === 'string' ? input : input.toString();
    if (url.endsWith('/api/v1/admin/instances')) {
      return json({
        instances: [{ name: 'radarr', type: 'radarr', public_url: 'https://radarr.example' }],
      });
    }
    if (url.includes('/collections/')) {
      return json({
        tmdb_collection_id: 726871, name: 'Dune Collection', poster: null,
        radarr_monitored: false,
        parts: [{ tmdb_id: 438631, title: 'Dune', year: 2021, in_library: true, movie_id: 1 }],
      });
    }
    if (url.includes('/cast')) {
      return json({
        tmdb_id: 438631,
        served_language: 'en',
        cast: [
          { person_id: 1, tmdb_id: 30614, name: 'Timothée Chalamet', character_name: 'Paul', credit_order: 0 },
        ],
      });
    }
    if (url.includes('/overview')) {
      return json({
        overview: 'Paul Atreides arrives on Arrakis.',
        tagline: 'Beyond fear, destiny awaits.',
        title: 'Dune',
        served_language: 'en',
      });
    }
    if (url.includes('/ratings')) {
      return json({
        tmdb_rating: 8.1, tmdb_votes: 12000,
        imdb_rating: 8.0, imdb_votes: 900000,
        rated: 'PG-13', awards: 'Won 6 Oscars',
        sources: { tmdb: 'fresh', omdb: 'fresh' },
      });
    }
    if (url.includes('/recommendations')) {
      return json({
        items: [{ tmdb_id: 370172, title: 'Dune: Part Two', year: 2024, tmdb_rating: 8.4, poster_asset: 'p1' }],
        degraded: [],
      });
    }
    // B1.5/ADR-0023 — see the spyFetch comment above for why these two
    // routes are needed. "not configured" keeps the panel a no-op for
    // every routedFetch-based test in this file today.
    if (url.includes('/qbit/settings')) {
      return json({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404);
    }
    if (url.includes('/torrents')) {
      return json({ torrents: [], synced_at: new Date().toISOString(), total_count: 0, live_count: 0 });
    }
    return json(base);
  }) as typeof fetch;
}

function renderRoute(path: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <PageTitleProvider defaultTitle="__INITIAL__">
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <TooltipProvider delayDuration={0}>
            <MemoryRouter initialEntries={[path]}>
              <AddToRadarrProvider>
                <Routes>
                  <Route path="/movies/:tmdbId" element={<MovieDetail />} />
                </Routes>
              </AddToRadarrProvider>
            </MemoryRouter>
          </TooltipProvider>
        </QueryClientProvider>
      </I18nextProvider>
    </PageTitleProvider>,
  );
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/movies/438631', search: '', assign: vi.fn() },
  });
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<MovieDetail />', () => {
  it('renders the hero, ratings, overview and library membership', async () => {
    spyFetch(movie());
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-page')).toBeInTheDocument();
    expect(screen.getByTestId('hero-title')).toHaveTextContent('Dune');
    expect(screen.getByText('Beyond fear, destiny awaits.')).toBeInTheDocument();
    expect(screen.getByTestId('rating-tmdb')).toHaveTextContent('8.1');
    expect(screen.getByTestId('rating-imdb')).toHaveTextContent('8.0');
    expect(screen.getByTestId('imdb-external-link')).toHaveAttribute(
      'href',
      'https://www.imdb.com/title/tt1160419/',
    );
    // Overview text paints from the base movie DTO on the first frame (the
    // localized /overview endpoint only refines it).
    expect(screen.getByTestId('overview-text')).toHaveTextContent('Arrakis');
    expect(screen.getByTestId('movie-library-row-radarr')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-monitored')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-hasfile')).toBeInTheDocument();
    // The on-disk strip now lives at the bottom of the hero, not in a
    // separate below-hero section.
    expect(screen.queryByTestId('movie-detail-library')).toBeNull();
    expect(screen.getByTestId('movie-hero-library-strip')).toBeInTheDocument();
  });

  it('renders quality/codec chip only when has_file and quality are present', async () => {
    spyFetch({
      ...movie(),
      library: [
        {
          instance_name: 'radarr',
          monitored: true,
          has_file: true,
          availability: 'released',
          quality: 'Bluray-1080p',
          resolution: 1080,
          video_codec: 'x265',
          audio_codec: 'EAC3',
        },
      ],
    });
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-library-row-radarr')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-quality')).toHaveTextContent('Bluray-1080p');
    expect(screen.getByTestId('movie-library-codec')).toHaveTextContent('x265 · EAC3');
  });

  it('omits the quality chip when the sync has not captured quality yet (has_file but no quality)', async () => {
    spyFetch({
      ...movie(),
      library: [
        { instance_name: 'radarr', monitored: true, has_file: true, availability: 'released' },
      ],
    });
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-library-row-radarr')).toBeInTheDocument();
    expect(screen.queryByTestId('movie-library-quality')).toBeNull();
    expect(screen.queryByTestId('movie-library-codec')).toBeNull();
  });

  it('composes all four movie sections (overview, cast, ratings, recommendations)', async () => {
    routedFetch();
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-page')).toBeInTheDocument();
    // Overview block (shared MediaDetail overview text, fed by the page).
    expect(await screen.findByTestId('overview-text')).toBeInTheDocument();
    // Cast strip (prop-driven, fed by useMovieCast).
    expect(await screen.findByTestId('cast-strip')).toBeInTheDocument();
    expect(screen.getByTestId('cast-strip-name')).toHaveTextContent('Timothée Chalamet');
    // View-all link — routes to the new full movie-cast page.
    const viewAll = screen.getByTestId('cast-strip-view-all');
    expect(viewAll).toBeInTheDocument();
    expect(viewAll.getAttribute('href')).toBe('/movies/438631/cast');
    // Ratings section (fed by useMovieRatings, now owned by the page).
    expect(await screen.findByTestId('ratings-section')).toBeInTheDocument();
    expect(screen.getByTestId('ratings-awards')).toHaveTextContent('Won 6 Oscars');
    // Recommendations rail (self-fetches /recommendations).
    expect(await screen.findByTestId('movie-recommendations')).toBeInTheDocument();
  });

  it('renders the right-rail sidebar (status, studio, country, language, keywords)', async () => {
    routedFetch();
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('rail-card')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-status')).toHaveTextContent('Released');
    expect(screen.getByTestId('rail-row-studio-value')).toHaveTextContent('Legendary Pictures');
    expect(screen.getByTestId('rail-row-countries')).toBeInTheDocument();
    expect(screen.getByTestId('rail-row-original-language')).toBeInTheDocument();
    expect(screen.getByTestId('rail-keywords')).toBeInTheDocument();
    expect(within(screen.getByTestId('rail-keywords')).getByText('desert')).toBeInTheDocument();
    // Digital/physical release date rows are absent when the DTO omits
    // those fields (base `movie()` fixture doesn't set them).
    expect(screen.queryByTestId('rail-row-digital-release')).toBeNull();
    expect(screen.queryByTestId('rail-row-physical-release')).toBeNull();
  });

  it('shows humanized vote counts alongside the hero TMDB/IMDb ratings', async () => {
    // routedFetch's /ratings stub carries tmdb_votes/imdb_votes — the
    // per-section rating numbers `toMovieVM` now wires into
    // `ratings.tmdb.votes` / `ratings.imdb.votes` (RatingDuo renders
    // "· <humanized votes>" whenever votes > 0 is present).
    routedFetch();
    renderRoute('/movies/438631');

    await waitFor(() => {
      expect(screen.getByTestId('rating-tmdb')).toHaveTextContent('· 12k');
    });
    expect(screen.getByTestId('rating-imdb')).toHaveTextContent('· 900k');
  });

  it('localizes the sidebar Status row instead of printing the raw TMDB value', async () => {
    // The raw TMDB status ("Released") IS already an English word, so an
    // English-locale assertion would pass whether or not real i18n lookup
    // happened. Switch to 'ru' and assert the Russian label to prove the
    // value routes through real i18n, not a pass-through of the raw string.
    await i18n.changeLanguage('ru');
    try {
      routedFetch();
      renderRoute('/movies/438631');

      const statusRow = await screen.findByTestId('rail-row-status');
      expect(statusRow).toHaveTextContent('Вышел');
      expect(statusRow).not.toHaveTextContent('Released');
    } finally {
      await i18n.changeLanguage('en');
    }
  });

  it('renders localized (formatted, not raw ISO) digital/physical release date rows when present', async () => {
    spyFetch({
      ...movie(),
      digital_release_date: '2022-01-15T00:00:00Z',
      physical_release_date: '2022-01-22T00:00:00Z',
    });
    renderRoute('/movies/438631');

    const digitalRow = await screen.findByTestId('rail-row-digital-release');
    expect(digitalRow.textContent ?? '').not.toBe('2022-01-15T00:00:00Z');
    expect(digitalRow).toHaveTextContent('2022');
    expect(digitalRow).toHaveTextContent(/15/);

    const physicalRow = await screen.findByTestId('rail-row-physical-release');
    expect(physicalRow.textContent ?? '').not.toBe('2022-01-22T00:00:00Z');
    expect(physicalRow).toHaveTextContent('2022');
    expect(physicalRow).toHaveTextContent(/22/);
  });

  it('opens the trailer modal from the hero trailer button', async () => {
    routedFetch();
    renderRoute('/movies/438631');

    const btn = await screen.findByTestId('hero-action-trailer');
    expect(screen.queryByTestId('trailer-modal-iframe')).toBeNull();
    btn.click();
    await waitFor(() => {
      expect(screen.getByTestId('trailer-modal-iframe')).toBeInTheDocument();
    });
  });

  it('renders the empty library note when the movie is in no library', async () => {
    spyFetch({ ...movie(), library: [] });
    renderRoute('/movies/438631');

    const empty = await screen.findByTestId('movie-detail-library-empty');
    expect(empty).toBeInTheDocument();
    // Lives inside the hero bottom strip, not a below-hero section.
    expect(screen.getByTestId('movie-hero')).toContainElement(empty);
    expect(screen.queryByTestId('movie-detail-library')).toBeNull();
  });

  it('renders the invalid-param branch for a non-numeric id', async () => {
    spyFetch(movie());
    renderRoute('/movies/not-a-number');

    expect(await screen.findByTestId('movie-detail-invalid')).toBeInTheDocument();
  });

  it('renders the load-error state on a failed fetch', async () => {
    spyFetch({ error: 'boom' }, 500);
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-error')).toBeInTheDocument();
  });

  it('renders the Add-to-Radarr split-button when the movie is in no library', async () => {
    spyFetch({ ...movie(), library: [] });
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-add-to-radarr')).toBeInTheDocument();
    expect(screen.getByTestId('movie-detail-add-to-radarr-primary')).toBeInTheDocument();
    expect(screen.queryByTestId('movie-detail-open-in-radarr')).toBeNull();
  });

  it('swaps in the Open-in-Radarr deep link when the movie is in a library', async () => {
    // Route the movie detail vs the instances roster by URL so the button can
    // resolve the radarr instance's operator-configured public_url.
    globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.endsWith('/api/v1/admin/instances')) {
        return json({
          instances: [{ name: 'radarr', type: 'radarr', public_url: 'https://radarr.example' }],
        });
      }
      return json(movie());
    }) as typeof fetch;
    renderRoute('/movies/438631');

    await screen.findByTestId('movie-detail-open-in-radarr');
    // The public_url resolves once the instances roster query settles, at which
    // point the CTA swaps from a disabled <button> to the deep-link <a>.
    await waitFor(() => {
      const open = screen.getByTestId('movie-detail-open-in-radarr');
      expect(open.tagName).toBe('A');
      expect(open).toHaveAttribute('href', 'https://radarr.example/movie/438631');
    });
    expect(screen.getByTestId('movie-detail-open-in-radarr')).toHaveAttribute('target', '_blank');
    expect(screen.queryByTestId('movie-detail-add-to-radarr-primary')).toBeNull();
  });

  it('explains the disabled Open-in-Radarr CTA when the instance has no public_url', async () => {
    // The instances roster resolves, but the holding radarr instance has no
    // operator-configured public_url — no deep link can be built.
    globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.endsWith('/api/v1/admin/instances')) {
        return json({ instances: [{ name: 'radarr', type: 'radarr', public_url: null }] });
      }
      return json(movie());
    }) as typeof fetch;
    renderRoute('/movies/438631');

    const open = await screen.findByTestId('movie-detail-open-in-radarr');
    await waitFor(() => {
      expect(open.tagName).toBe('BUTTON');
      expect(open).toBeDisabled();
    });
    expect(open.closest('span')).toHaveAttribute(
      'title',
      'Radarr public URL is not configured',
    );
  });

  it('formats the sidebar premiere date instead of printing the raw ISO date', async () => {
    // Decision B (ADR-0022 Wave-2 Story C) — the hero release-date display
    // is REPLACED by a `rail-row-premiere-date` sidebar fact, rendered via
    // the shared `<PremiereDate>` leaf. The BE marshals `Released` as a
    // full RFC3339 timestamp (dto_movie_detail.go's `*time.Time`), so this
    // fixture uses the REAL wire shape — `toMovieVM` must truncate it to a
    // calendar date before handing it to `<PremiereDate>`.
    spyFetch({ ...movie(), release_date: '2024-11-13T00:00:00Z' });
    renderRoute('/movies/438631');

    const premiereRow = await screen.findByTestId('rail-row-premiere-date');
    expect(premiereRow.textContent ?? '').not.toBe('2024-11-13T00:00:00Z');
    // `<PremiereDate>` renders a locale-formatted calendar date (not the
    // raw wire timestamp) — assert both date components it must preserve.
    expect(premiereRow).toHaveTextContent('2024');
    expect(premiereRow).toHaveTextContent(/13/);
  });

  it('omits the hero collection card when the movie has no collection', async () => {
    spyFetch(movie());
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-page')).toBeInTheDocument();
    expect(screen.queryByTestId('hero-next-wrap')).toBeNull();
    expect(screen.queryByTestId('movie-collection-hero-card')).toBeNull();
    expect(screen.queryByTestId('movie-collection-block')).toBeNull();
  });

  it('renders the compact hero collection card only when collection.tmdb_collection_id is present', async () => {
    // Route the movie detail vs the collection detail vs instances by URL.
    globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/collections/')) {
        return json({
          tmdb_collection_id: 726871, name: 'Dune Collection', poster: null,
          radarr_monitored: false,
          parts: [{ tmdb_id: 438631, title: 'Dune', year: 2021, in_library: true, movie_id: 1 }],
        });
      }
      if (url.endsWith('/api/v1/admin/instances')) {
        return json({ instances: [] });
      }
      return json({
        ...movie(),
        collection: { tmdb_collection_id: 726871, name: 'Dune Collection', radarr_monitored: false },
      });
    }) as typeof fetch;
    renderRoute('/movies/438631');

    const nextWrap = await screen.findByTestId('hero-next-wrap');
    const card = await screen.findByTestId('movie-collection-hero-card');
    expect(nextWrap).toContainElement(card);
    expect(screen.getByTestId('movie-collection-hero-name')).toHaveTextContent('Dune Collection');
    expect(screen.getByTestId('movie-collection-hero-poster')).toBeInTheDocument();
    expect(screen.getByTestId('movie-collection-hero-toggle')).toBeInTheDocument();
    expect(screen.getByTestId('movie-collection-hero-add-all'))
      .toHaveTextContent(i18n.t('movieCollection.addAll'));
    // No wide below-hero block anymore.
    expect(screen.queryByTestId('movie-collection-block')).toBeNull();
  });

  it('renders the movie torrents panel with a provenance chip and the hero view-torrents action', async () => {
    globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/qbit/settings')) {
        return json({ enabled: true, url: 'http://qbit', username: 'u' });
      }
      if (url.includes('/torrents')) {
        return json({
          torrents: [
            { hash: 'a', name: 'dune.2021.bluray', size_bytes: 4_000_000_000, present: true, live: true, ratio: 0, provenance: 'manual_import' },
          ],
          synced_at: new Date().toISOString(),
        });
      }
      if (url.endsWith('/api/v1/admin/instances')) {
        return json({ instances: [{ name: 'radarr', type: 'radarr', public_url: 'https://radarr.example' }] });
      }
      return json(movie());
    }) as typeof fetch;
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('torrents-section')).toBeInTheDocument();
    // jsdom does not evaluate the `hidden md:block` / `md:hidden`
    // responsive classes, so BOTH the desktop table row and the mobile
    // card render simultaneously — use getAllByTestId (not the singular
    // getByTestId) and assert on the first match. Deviation from the
    // story's exact test code (B1.5/ADR-0023 impl report).
    const chips = await screen.findAllByTestId('torrent-provenance');
    expect(chips[0]).toHaveTextContent('manual import');

    fireEvent.click(screen.getByTestId('movie-detail-view-torrents'));
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
  });
});
