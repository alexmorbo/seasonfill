import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import i18n from '@/i18n';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { Person } from './Person';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function renderRoute(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } } });
  return render(
    <PageTitleProvider defaultTitle="__INITIAL__">
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={[path]}>
            <Routes>
              <Route path="/person/:tmdbId" element={<Person />} />
              <Route path="/series/:instance/:id" element={<div>SD</div>} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>
      </I18nextProvider>
    </PageTitleProvider>,
  );
}

const fullFixture = {
  person: {
    id: 7, tmdb_id: 4495, name: 'Pedro Pascal',
    known_for_department: 'Acting',
    birthday: '1975-04-02', place_of_birth: 'Santiago, Chile',
    profile_asset: 'aaaa',
  },
  biography: 'Chilean-American actor.',
  bio_language: 'en-US',
  sync: { source: 'tmdb_person', synced_at: new Date().toISOString() },
  library_credits: [
    { series_id: 42, title: 'The Last of Us', year: 2023,
      role_label: 'Joel Miller · 9 ep.', kind: 'cast',
      instances: [{ instance: 'alpha', sonarr_series_id: 7001 }], poster_asset: 'pp1' },
  ],
  other_credits: [
    { tmdb_media_id: 999, title: 'Strange Way of Life', year: 2023,
      media_type: 'tv', kind: 'cast', role_label: 'Silva',
      poster_asset: 'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' },
  ],
  degraded: [],
};

describe('<Person />', () => {
  beforeEach(() => mockApi.mockReset());

  it('renders skeleton while loading', () => {
    mockApi.mockReturnValueOnce(new Promise(() => {}));
    renderRoute('/person/4495');
    expect(screen.getByTestId('person-skeleton')).toBeInTheDocument();
  });

  it('renders hero + biography + library + other sections after data loads', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/person/4495');
    await waitFor(() => expect(screen.getByTestId('person-hero-name')).toBeInTheDocument());
    expect(screen.getByTestId('person-biography')).toBeInTheDocument();
    expect(screen.getByTestId('person-library-section')).toBeInTheDocument();
    expect(screen.getByTestId('person-other-section')).toBeInTheDocument();
  });

  it('hides In-your-library when library_credits is empty', async () => {
    mockApi.mockResolvedValueOnce({ ...fullFixture, library_credits: [] });
    renderRoute('/person/4495');
    await waitFor(() => expect(screen.getByTestId('person-hero-name')).toBeInTheDocument());
    expect(screen.queryByTestId('person-library-section')).toBeNull();
  });

  it('hides Other titles when other_credits is empty', async () => {
    mockApi.mockResolvedValueOnce({ ...fullFixture, other_credits: [] });
    renderRoute('/person/4495');
    await waitFor(() => expect(screen.getByTestId('person-hero-name')).toBeInTheDocument());
    expect(screen.queryByTestId('person-other-section')).toBeNull();
  });

  it('shows the stub note when degraded contains tmdb_person', async () => {
    mockApi.mockResolvedValueOnce({ ...fullFixture, degraded: ['tmdb_person'] });
    renderRoute('/person/4495');
    await waitFor(() => expect(screen.getByTestId('person-stub-note')).toBeInTheDocument());
  });

  it('shows the limited-data note when payload is empty AND not stub', async () => {
    mockApi.mockResolvedValueOnce({ person: {}, degraded: [] });
    renderRoute('/person/4495');
    await waitFor(() => expect(screen.getByTestId('person-limited')).toBeInTheDocument());
  });

  it('shows the error alert on a failed fetch', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderRoute('/person/4495');
    await waitFor(() => expect(screen.getByTestId('person-error')).toBeInTheDocument());
  });

  it('renders the invalid-tmdb alert when URL param is NaN', () => {
    renderRoute('/person/not-a-number');
    expect(screen.getByText(/Invalid person link/i)).toBeInTheDocument();
    expect(mockApi).not.toHaveBeenCalled();
  });
});
