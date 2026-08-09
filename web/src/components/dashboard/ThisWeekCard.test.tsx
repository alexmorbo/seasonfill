import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { ThisWeekCard } from './ThisWeekCard';

const origFetch = globalThis.fetch;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function spyFetch(body: unknown, status = 200) {
  const urls: string[] = [];
  const fn = vi.fn(async (url: RequestInfo | URL) => {
    urls.push(typeof url === 'string' ? url : url.toString());
    return json(body, status);
  });
  globalThis.fetch = fn as typeof fetch;
  return urls;
}

function weekReport() {
  return {
    generated_at: new Date().toISOString(),
    days: [
      {
        date: new Date().toISOString().slice(0, 10),
        events: [
          {
            series_id: 42,
            title: 'The Expanse',
            season: 2,
            episode: 1,
            air_date: new Date().toISOString(),
            state: 'downloaded',
            in_library_instances: ['main'],
            season_premiere: true,
            milestone: 'premiere',
            media_type: 'tv',
          },
        ],
      },
    ],
  };
}

afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<ThisWeekCard />', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/', search: '', assign: vi.fn() },
    });
  });

  it('renders this-week rows and queries with from/to/scope=all', async () => {
    const urls = spyFetch(weekReport());
    renderWithProviders(<ThisWeekCard />, { route: '/' });

    expect(await screen.findByTestId('this-week-list')).toBeInTheDocument();
    expect(screen.getByText(/The Expanse/)).toBeInTheDocument();

    await vi.waitFor(() => {
      const u = urls.find((x) => x.includes('/calendar'));
      expect(u).toBeDefined();
      expect(u).toContain('scope=all');
      expect(u).toContain('from=');
      expect(u).toContain('to=');
    });
  });

  it('shows the empty branch when nothing airs this week', async () => {
    spyFetch({ generated_at: new Date().toISOString(), days: [] });
    renderWithProviders(<ThisWeekCard />, { route: '/' });
    expect(await screen.findByTestId('this-week-empty')).toBeInTheDocument();
  });

  it('shows the error branch on failure', async () => {
    spyFetch({ error: 'boom' }, 500);
    renderWithProviders(<ThisWeekCard />, { route: '/' });
    expect(await screen.findByTestId('this-week-error')).toBeInTheDocument();
  });
});
