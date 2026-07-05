import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { SeriesCard } from './SeriesCard';
import type { DiscoverySeriesItem } from '@/api/discovery';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const mod = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...mod, useNavigate: () => mockNavigate };
});

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider delayDuration={0}>
        <MemoryRouter>{node}</MemoryRouter>
      </TooltipProvider>
    </I18nextProvider>,
  );
}

beforeEach(() => {
  mockNavigate.mockClear();
  mockApi.mockReset();
});

describe('<SeriesCard />', () => {
  it('renders the title below and the year as a bottom-left poster overlay', () => {
    r(<SeriesCard title="Breaking Bad" year={2008} seriesId={7} />);
    expect(screen.getByTestId('series-card-title')).toHaveTextContent('Breaking Bad');
    const yr = screen.getByTestId('series-card-year');
    expect(yr).toHaveTextContent('2008');
    expect(yr.className).toContain('absolute');
    expect(yr.className).toContain('bottom-2');
    expect(yr.className).toContain('left-2');
  });

  it('shows the ★ rating as a bottom-right poster overlay when rating > 0', () => {
    r(<SeriesCard title="Show" year={2020} rating={8.4} seriesId={1} />);
    const rt = screen.getByTestId('series-card-rating');
    expect(rt).toHaveTextContent('8.4');
    expect(rt.className).toContain('absolute');
    expect(rt.className).toContain('bottom-2');
    expect(rt.className).toContain('right-2');
  });

  it('hides the year overlay when year is absent', () => {
    r(<SeriesCard title="Show" rating={8.4} seriesId={1} />);
    expect(screen.queryByTestId('series-card-year')).toBeNull();
  });

  it('renders no bottom overlays when neither year nor rating is present', () => {
    r(<SeriesCard title="Show" seriesId={1} />);
    expect(screen.queryByTestId('series-card-year')).toBeNull();
    expect(screen.queryByTestId('series-card-rating')).toBeNull();
  });

  it('hides the ★ rating when rating is absent or 0', () => {
    const { rerender } = r(<SeriesCard title="Show" year={2020} seriesId={1} />);
    expect(screen.queryByTestId('series-card-rating')).toBeNull();
    rerender(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter>
            <SeriesCard title="Show" year={2020} rating={0} seriesId={1} />
          </MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>,
    );
    expect(screen.queryByTestId('series-card-rating')).toBeNull();
    // year still shown, no dash artifact
    expect(screen.getByTestId('series-card')).toHaveTextContent('2020');
  });

  it('links directly to /series/:id when seriesId is set', () => {
    r(<SeriesCard title="Show" seriesId={42} />);
    const card = screen.getByTestId('series-card');
    expect(card.tagName.toLowerCase()).toBe('a');
    expect(card.getAttribute('href')).toBe('/series/42');
  });

  it('resolves a tmdbId then navigates to the resolved series id', async () => {
    mockApi.mockResolvedValueOnce({ series_id: 99 });
    r(<SeriesCard title="Show" tmdbId={555} />);
    const card = screen.getByTestId('series-card');
    expect(card.tagName.toLowerCase()).not.toBe('a');
    await userEvent.click(card);
    await waitFor(() => {
      expect(mockApi).toHaveBeenCalledWith('/series/resolve?tmdb_id=555');
      expect(mockNavigate).toHaveBeenCalledWith('/series/99');
    });
  });

  it('does not crash when the resolve call fails', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    r(<SeriesCard title="Show" tmdbId={7} />);
    await userEvent.click(screen.getByTestId('series-card'));
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockNavigate).not.toHaveBeenCalled();
    expect(screen.getByTestId('series-card')).toBeInTheDocument();
  });

  it('renders the missing chip only when missingCount > 0', () => {
    const { rerender } = r(<SeriesCard title="Show" seriesId={1} />);
    expect(screen.queryByTestId('series-card-missing-chip')).toBeNull();
    rerender(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter>
            <SeriesCard title="Show" seriesId={1} missingCount={3} />
          </MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('series-card-missing-chip')).toHaveTextContent('3');
  });

  it('renders the character line only when characterName is set', () => {
    r(<SeriesCard title="Show" seriesId={1} characterName="Walter White" />);
    expect(screen.getByTestId('series-card-character')).toHaveTextContent('Walter White');
  });

  it('renders the library badge only when libraryBadge is set', () => {
    const { rerender } = r(<SeriesCard title="Show" seriesId={1} />);
    expect(screen.queryByTestId('series-card-library-badge')).toBeNull();
    rerender(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter>
            <SeriesCard title="Show" seriesId={1} libraryBadge="inLibrary" />
          </MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('series-card-library-badge')).toBeInTheDocument();
  });

  it('renders the Add-to-Sonarr button when addToSonarr is provided', () => {
    const item: DiscoverySeriesItem = {
      series_id: 0,
      tmdb_id: 123,
      title: 'Show',
      in_library_instances: [],
    };
    r(<SeriesCard title="Show" tmdbId={123} addToSonarr={item} />);
    expect(screen.getByTestId('add-to-sonarr-button')).toBeInTheDocument();
  });

  it('renders the footer slot when provided', () => {
    r(
      <SeriesCard
        title="Show"
        seriesId={1}
        footer={<span data-testid="custom-footer">alpha</span>}
      />,
    );
    expect(screen.getByTestId('custom-footer')).toHaveTextContent('alpha');
  });

  it('renders none of the removed affordances (green dot, sonarr link, timestamp, tmdb-only badge)', () => {
    r(<SeriesCard title="Show" year={2020} rating={7} seriesId={1} />);
    const card = screen.getByTestId('series-card');
    expect(card.querySelector('[data-testid="sonarr-link"]')).toBeNull();
    expect(card.querySelector('[data-testid*="monitored"]')).toBeNull();
    expect(card.querySelector('.bg-ok')).toBeNull();
    expect(card).not.toHaveTextContent(/ago|назад/i);
    expect(card).not.toHaveTextContent(/TMDB only|ТОЛЬКО TMDB/i);
  });
});
