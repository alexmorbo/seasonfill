import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { FilteredPanel } from './FilteredPanel';
import { useDiscoverFilter } from '@/hooks/useDiscoverFilter';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function Harness() {
  const state = useDiscoverFilter();
  return (
    <div>
      <div data-testid="state-mirror">{JSON.stringify(state.filter)}</div>
      <div data-testid="has-active">{String(state.hasActiveFilter)}</div>
      <FilteredPanel state={state} />
    </div>
  );
}

function renderPanel(initial = '/?tab=filtered') {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={[initial]}><Harness /></MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const genresPayload = {
  items: [
    { id: 18, name: 'Drama' },
    { id: 35, name: 'Comedy' },
  ],
};
const networksPayload = {
  items: [
    { id: 49, name: 'HBO' },
    { id: 213, name: 'Netflix' },
  ],
};

beforeEach(() => {
  mockApi.mockReset();
  mockApi.mockImplementation((p: string) => {
    if (p.startsWith('/discovery/genres')) return Promise.resolve(genresPayload);
    if (p.startsWith('/discovery/networks')) return Promise.resolve(networksPayload);
    return Promise.resolve({ items: [] });
  });
});

describe('<FilteredPanel />', () => {
  it('renders chips after genres + networks resolve', async () => {
    renderPanel();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filter-genres')).toBeInTheDocument());
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filter-networks')).toBeInTheDocument());
    expect(screen.getByText('Drama')).toBeInTheDocument();
    expect(screen.getByText('HBO')).toBeInTheDocument();
  });

  it('clicking a genre chip toggles it into the filter', async () => {
    renderPanel();
    const drama = await waitFor(() => screen.getByText('Drama').closest('button')!);
    fireEvent.click(drama);
    await waitFor(() => {
      expect(screen.getByTestId('has-active').textContent).toBe('true');
      expect(JSON.parse(screen.getByTestId('state-mirror').textContent || '{}'))
        .toEqual({ with_genres: [18] });
    });
    // Click again — toggles off.
    fireEvent.click(screen.getByText('Drama').closest('button')!);
    await waitFor(() => {
      expect(screen.getByTestId('has-active').textContent).toBe('false');
    });
  });

  it('status + type chips also update state', async () => {
    renderPanel();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filter-statuses')).toBeInTheDocument());
    const statusChips = screen.getAllByTestId('discovery-filter-status-chip');
    fireEvent.click(statusChips[0]!); // returning
    const typeChips = screen.getAllByTestId('discovery-filter-type-chip');
    fireEvent.click(typeChips[1]!); // documentary
    await waitFor(() => {
      const s = JSON.parse(screen.getByTestId('state-mirror').textContent || '{}');
      expect(s.with_status).toEqual(['returning']);
      expect(s.with_type).toEqual(['documentary']);
    });
  });

  it('year-min slider patches first_air_date_gte', async () => {
    renderPanel();
    const yMin = await waitFor(() => screen.getByTestId('discovery-filter-year-min'));
    fireEvent.change(yMin, { target: { value: '1990' } });
    await waitFor(() => {
      const s = JSON.parse(screen.getByTestId('state-mirror').textContent || '{}');
      expect(s.first_air_date_gte).toBe('1990-01-01');
    });
  });

  it('rating-max slider patches vote_average_lte', async () => {
    renderPanel();
    const rMax = await waitFor(() => screen.getByTestId('discovery-filter-rating-max'));
    fireEvent.change(rMax, { target: { value: '7.5' } });
    await waitFor(() => {
      const s = JSON.parse(screen.getByTestId('state-mirror').textContent || '{}');
      expect(s.vote_average_lte).toBe(7.5);
    });
  });

  it('Clear button is disabled when no active filter', async () => {
    renderPanel();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filter-clear'))
        .toBeDisabled());
  });

  it('Clear button empties the filter state', async () => {
    renderPanel('/?tab=filtered&with_genres=18,35');
    const clear = await waitFor(() => {
      const btn = screen.getByTestId('discovery-filter-clear');
      expect(btn).not.toBeDisabled();
      return btn;
    });
    fireEvent.click(clear);
    await waitFor(() => {
      expect(screen.getByTestId('has-active').textContent).toBe('false');
    });
  });
});
