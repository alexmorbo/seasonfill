import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DiscoveryRails } from './DiscoveryRails';
import type { DiscoveryRow } from '@/api/discoveryRows';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

// Stub the child so this test isolates config → rails mapping (empty rails
// would otherwise return null and vanish).
vi.mock('./DiscoveryRail', () => ({
  DiscoveryRail: ({ row }: { row: DiscoveryRow }) => (
    <div data-testid={`discovery-rail-${row.row_type}`} data-position={row.position}>
      {row.title}
    </div>
  ),
}));

function renderRails() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <DiscoveryRails />
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const DEFAULT_ROWS: DiscoveryRow[] = [
  { row_type: 'trending', source: 'tmdb_discover', media_type: 'tv', params: {}, position: 0, enabled: true, title: 'Тренды' },
  { row_type: 'popular', source: 'tmdb_discover', media_type: 'tv', params: {}, position: 1, enabled: true, title: 'Популярное' },
  { row_type: 'upcoming', source: 'tmdb_discover', media_type: 'tv', params: { sort_by: 'first_air_date.desc' }, position: 2, enabled: true, title: 'Новые сериалы' },
  { row_type: 'recently_added', source: 'library', media_type: 'tv', params: {}, position: 3, enabled: true, title: 'Недавно добавленное' },
  { row_type: 'upcoming_releases', source: 'tmdb_discover', media_type: 'tv', params: { sort_by: 'first_air_date.desc' }, position: 4, enabled: true, title: 'Скоро на экраны' },
  { row_type: 'genre', source: 'tmdb_discover', media_type: 'tv', params: { with_genres: '18', sort_by: 'popularity.desc' }, position: 5, enabled: true, title: 'Драмы' },
  { row_type: 'network', source: 'tmdb_discover', media_type: 'tv', params: { with_networks: '213', sort_by: 'popularity.desc' }, position: 6, enabled: true, title: 'Netflix' },
];

beforeEach(() => mockApi.mockReset());

describe('<DiscoveryRails />', () => {
  it('renders one rail per enabled row in position order', async () => {
    mockApi.mockResolvedValueOnce({ rows: DEFAULT_ROWS });
    renderRails();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-rails')).toBeInTheDocument());
    const rails = screen.getAllByTestId(/^discovery-rail-/);
    expect(rails).toHaveLength(7);
    const positions = rails.map((el) => Number(el.getAttribute('data-position')));
    expect(positions).toEqual([0, 1, 2, 3, 4, 5, 6]);
    expect(mockApi).toHaveBeenCalledWith('/discovery/rows');
  });

  it('drops disabled rows', async () => {
    mockApi.mockResolvedValueOnce({
      rows: [
        DEFAULT_ROWS[0],
        { ...DEFAULT_ROWS[1], enabled: false },
      ],
    });
    renderRails();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-rail-trending')).toBeInTheDocument());
    expect(screen.queryByTestId('discovery-rail-popular')).toBeNull();
  });

  it('renders an error alert when the config fetch fails', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderRails();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-rails-error')).toBeInTheDocument());
  });
});
