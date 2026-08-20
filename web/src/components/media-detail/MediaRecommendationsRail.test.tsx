import { useRef } from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { MediaRecommendationsRail, type MediaRecommendationsRailProps } from './MediaRecommendationsRail';
import type { RecommendationItem } from './view-model';

function stubRenderCard(item: RecommendationItem, idx: number) {
  return (
    <div key={item.series_id ?? item.tmdb_series_id ?? idx} data-testid="series-card">
      {item.title}
    </div>
  );
}

function Harness(props: Omit<MediaRecommendationsRailProps, 'sentinelRef'>) {
  const ref = useRef<HTMLElement | null>(null);
  return <MediaRecommendationsRail {...props} sentinelRef={ref} />;
}

function wrap(ui: React.ReactElement) {
  return <MemoryRouter>{ui}</MemoryRouter>;
}

const items: RecommendationItem[] = [
  { series_id: 1, title: 'Show A', year: 2022, tmdb_rating: 8.1, poster_asset: 'a', in_library: true },
  { series_id: 2, title: 'Show B', year: 2021, tmdb_rating: 7.6, poster_asset: 'b', in_library: false },
];

describe('<MediaRecommendationsRail />', () => {
  it('renders the sentinel when items=[] and not loading and not visible', () => {
    render(wrap(
      <Harness
        items={[]} isLoading={false} visible={false}
        renderCard={stubRenderCard} label="Recommendations" loadingLabel="Loading…"
      />,
    ));
    expect(screen.getByTestId('recommendations-carousel-sentinel')).toBeInTheDocument();
  });

  it('renders a card per item via renderCard once visible with data', () => {
    render(wrap(
      <Harness
        items={items} isLoading={false} visible
        renderCard={stubRenderCard} label="Recommendations" loadingLabel="Loading…"
      />,
    ));
    expect(screen.getByTestId('recommendations-carousel')).toBeInTheDocument();
    expect(screen.getAllByTestId('series-card')).toHaveLength(2);
    expect(screen.getByText('Show A')).toBeInTheDocument();
    expect(screen.getByText('Show B')).toBeInTheDocument();
  });

  it('renders 6 skeleton tiles + loading label when isLoading and items=[]', () => {
    render(wrap(
      <Harness
        items={[]} isLoading visible
        renderCard={stubRenderCard} label="Recommendations" loadingLabel="Loading…"
      />,
    ));
    expect(screen.getByTestId('recommendations-carousel-loading')).toBeInTheDocument();
    expect(screen.getAllByTestId('recommendations-skeleton-tile')).toHaveLength(6);
    expect(screen.getByTestId('recommendations-loading-label')).toHaveTextContent('Loading…');
  });

  it('returns null when items=[] and not loading but visible', () => {
    const { container } = render(wrap(
      <Harness
        items={[]} isLoading={false} visible
        renderCard={stubRenderCard} label="Recommendations" loadingLabel="Loading…"
      />,
    ));
    expect(container.firstChild).toBeNull();
  });

  it('sets data-visible to reflect the visible prop', () => {
    render(wrap(
      <Harness
        items={items} isLoading={false} visible={false}
        renderCard={stubRenderCard} label="Recommendations" loadingLabel="Loading…"
      />,
    ));
    expect(screen.getByTestId('recommendations-carousel').getAttribute('data-visible')).toBe('false');
  });

  it('renders the heading label and an optional staleBadge', () => {
    render(wrap(
      <Harness
        items={items} isLoading={false} visible
        renderCard={stubRenderCard} label="Recommendations" loadingLabel="Loading…"
        staleBadge={<span data-testid="stale-badge">stale</span>}
      />,
    ));
    expect(screen.getByText('Recommendations')).toBeInTheDocument();
    expect(screen.getByTestId('stale-badge')).toBeInTheDocument();
  });

  it('slices items to the limit', () => {
    const many: RecommendationItem[] = Array.from({ length: 5 }, (_, i) => ({ series_id: i, title: `S${i}` }));
    render(wrap(
      <Harness
        items={many} isLoading={false} visible
        renderCard={stubRenderCard} label="Recommendations" loadingLabel="Loading…"
        limit={3}
      />,
    ));
    expect(screen.getAllByTestId('series-card')).toHaveLength(3);
  });
});
