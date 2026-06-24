import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { useState } from 'react';
import i18n from '@/i18n';
import { GenreFilter } from './GenreFilter';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function Harness({ initial = null as number | null }) {
  const [sel, setSel] = useState<number | null>(initial);
  return (
    <div>
      <div data-testid="sel-mirror">{sel === null ? 'none' : String(sel)}</div>
      <GenreFilter selectedGenreId={sel} onSelect={setSel} />
    </div>
  );
}

function renderFilter(initial: number | null = null) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <Harness initial={initial} />
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sample = {
  items: [
    { id: 18, name: 'Drama' },
    { id: 35, name: 'Comedy' },
    { id: 10765, name: 'Sci-Fi & Fantasy' },
  ],
};

beforeEach(() => mockApi.mockReset());

describe('<GenreFilter />', () => {
  it('renders chips and toggles selection on click', async () => {
    mockApi.mockResolvedValueOnce(sample);
    renderFilter();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-genres-chips')).toBeInTheDocument());
    const drama = screen.getByText('Drama').closest('button')!;
    fireEvent.click(drama);
    expect(screen.getByTestId('sel-mirror').textContent).toBe('18');
    expect(drama.getAttribute('aria-pressed')).toBe('true');
    // Clicking the same chip clears selection.
    fireEvent.click(drama);
    expect(screen.getByTestId('sel-mirror').textContent).toBe('none');
  });

  it('switches selection when another chip clicked', async () => {
    mockApi.mockResolvedValueOnce(sample);
    renderFilter(18);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-genres-chips')).toBeInTheDocument());
    const comedy = screen.getByText('Comedy').closest('button')!;
    fireEvent.click(comedy);
    expect(screen.getByTestId('sel-mirror').textContent).toBe('35');
  });

  it('renders skeleton while loading', () => {
    mockApi.mockReturnValueOnce(new Promise(() => {})); // never resolves
    renderFilter();
    expect(screen.getByTestId('discovery-genres-skeleton')).toBeInTheDocument();
  });

  it('renders empty state when list is empty', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    renderFilter();
    await waitFor(() => {
      expect(screen.queryByTestId('discovery-genres-chips')).toBeNull();
      expect(screen.queryByTestId('discovery-genres-skeleton')).toBeNull();
    });
    expect(screen.getByRole('heading', { level: 3 })).toBeInTheDocument();
  });
});
