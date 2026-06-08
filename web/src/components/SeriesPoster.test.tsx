import { describe, expect, it } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { SeriesPoster } from './SeriesPoster';

describe('<SeriesPoster />', () => {
  it('renders the proxy URL via buildPosterUrl when instance + seriesId provided', () => {
    render(<SeriesPoster instance="homelab" seriesId={122} title="For All Mankind" />);
    const img = screen.getByTestId('series-poster-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe(
      '/api/v1/instances/homelab/series/122/poster?size=full',
    );
    expect(img.getAttribute('loading')).toBe('lazy');
    expect(img.getAttribute('decoding')).toBe('async');
  });

  it('forwards the small size variant', () => {
    render(<SeriesPoster instance="homelab" seriesId={7} title="x" size="small" />);
    const img = screen.getByTestId('series-poster-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe(
      '/api/v1/instances/homelab/series/7/poster?size=small',
    );
  });

  it('omits the img when instance is empty', () => {
    render(<SeriesPoster instance="" seriesId={122} title="t" />);
    expect(screen.queryByTestId('series-poster-img')).toBeNull();
  });

  it('omits the img when seriesId is non-positive', () => {
    render(<SeriesPoster instance="x" seriesId={0} title="t" />);
    expect(screen.queryByTestId('series-poster-img')).toBeNull();
  });

  it('hides the img and reveals the gradient on error', () => {
    render(<SeriesPoster instance="homelab" seriesId={1} title="t" data-testid="wrap" />);
    const img = screen.getByTestId('series-poster-img');
    fireEvent.error(img);
    expect(screen.queryByTestId('series-poster-img')).toBeNull();
    expect(screen.getByTestId('wrap')).toBeInTheDocument();
  });
});
