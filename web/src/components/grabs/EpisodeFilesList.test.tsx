import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { EpisodeFilesList } from './EpisodeFilesList';

const origFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = origFetch; });

function wrap(ui: ReactElement): ReactNode {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  globalThis.fetch = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        items: [
          {
            id: 7001, relative_path: 'Season 02/Severance.S02E01.mkv',
            season_number: 2, episode_numbers: [1],
            size_bytes: 13_325_829_734, quality: 'WEBDL-2160p',
          },
          {
            id: 7002, relative_path: 'Season 02/Severance.S02E02.mkv',
            season_number: 2, episode_numbers: [2],
            size_bytes: 12_100_000_000, quality: 'WEBDL-2160p',
          },
        ],
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    ),
  ) as typeof fetch;
});

describe('<EpisodeFilesList />', () => {
  it('skips fetch when open=false', async () => {
    render(wrap(<EpisodeFilesList instance="alpha" grabId="g1" grabStatus="imported" open={false} />));
    await new Promise((r) => setTimeout(r, 10));
    expect(globalThis.fetch).not.toHaveBeenCalled();
  });

  it('renders rows when open=true', async () => {
    render(wrap(
      <EpisodeFilesList instance="alpha" grabId="g1" grabStatus="imported" open={true} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('episode-file-7001')).toBeInTheDocument();
    });
    expect(screen.getByText(/Severance\.S02E01\.mkv/)).toBeInTheDocument();
    expect(screen.getByText('S02E01')).toBeInTheDocument();
  });

  it('renders empty state when items=[] and status=imported', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    render(wrap(
      <EpisodeFilesList instance="alpha" grabId="g1" grabStatus="imported" open={true} />,
    ));
    await waitFor(() => {
      expect(screen.getByText(/Sonarr|empty/i)).toBeInTheDocument();
    });
  });

  it('renders "not imported" copy when status=grabbed', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    render(wrap(
      <EpisodeFilesList instance="alpha" grabId="g1" grabStatus="grabbed" open={true} />,
    ));
    await waitFor(() => {
      expect(screen.getByText(/not imported|не импортирован/i)).toBeInTheDocument();
    });
  });

  it('collapses past 5 files and expands on click', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          items: Array.from({ length: 8 }, (_, i) => ({
            id: 7000 + i + 1,
            relative_path: `Season 02/Severance.S02E0${i + 1}.mkv`,
            season_number: 2, episode_numbers: [i + 1],
            size_bytes: 1_000_000_000, quality: 'WEBDL-2160p',
          })),
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as typeof fetch;
    render(wrap(
      <EpisodeFilesList instance="alpha" grabId="g1" grabStatus="imported" open={true} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('episode-file-7001')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('episode-file-7006')).toBeNull();
    fireEvent.click(screen.getByText(/3 more|ещё 3/i));
    expect(screen.getByTestId('episode-file-7006')).toBeInTheDocument();
  });
});
