import { describe, expect, it } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MediaImage } from './MediaImage';

describe('<MediaImage />', () => {
  it('renders <img> with /api/v1/media/<hash> when hash is present', () => {
    render(
      <MediaImage
        hash="abc123def456"
        kind="series_poster"
        title="For All Mankind"
        fallback="monogram"
      />,
    );
    const img = screen.getByTestId('media-image-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/v1/media/abc123def456');
    expect(img.getAttribute('loading')).toBe('lazy');
    expect(img.getAttribute('decoding')).toBe('async');
  });

  it('renders monogram fallback when hash is undefined', () => {
    render(
      <MediaImage
        hash={undefined}
        kind="series_poster"
        title="Breaking Bad"
        fallback="monogram"
      />,
    );
    expect(screen.queryByTestId('media-image-img')).toBeNull();
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });

  it('renders monogram fallback when hash is null', () => {
    render(
      <MediaImage
        hash={null}
        kind="series_poster"
        title="The Sopranos"
        fallback="monogram"
      />,
    );
    expect(screen.queryByTestId('media-image-img')).toBeNull();
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });

  it('renders monogram fallback when hash is empty string', () => {
    render(
      <MediaImage
        hash=""
        kind="series_poster"
        title="Severance"
        fallback="monogram"
      />,
    );
    expect(screen.queryByTestId('media-image-img')).toBeNull();
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });

  it('swaps to monogram fallback on <img> onError', () => {
    render(
      <MediaImage
        hash="deadbeef"
        kind="series_poster"
        title="Andor"
        fallback="monogram"
      />,
    );
    const img = screen.getByTestId('media-image-img');
    fireEvent.error(img);
    expect(screen.queryByTestId('media-image-img')).toBeNull();
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });

  it('renders svg fallback when fallback=svg and hash is absent', () => {
    render(
      <MediaImage
        hash={undefined}
        kind="backdrop"
        title="Backdrop"
        fallback="svg"
      />,
    );
    expect(screen.queryByTestId('monogram-fallback')).toBeNull();
    expect(screen.getByTestId('media-image-svg-fallback')).toBeInTheDocument();
  });

  it('swaps to svg fallback on <img> onError when fallback=svg', () => {
    render(
      <MediaImage
        hash="abc"
        kind="backdrop"
        title="t"
        fallback="svg"
      />,
    );
    const img = screen.getByTestId('media-image-img');
    fireEvent.error(img);
    expect(screen.queryByTestId('media-image-img')).toBeNull();
    expect(screen.getByTestId('media-image-svg-fallback')).toBeInTheDocument();
  });

  it('uses hueKey for monogram when provided', () => {
    render(
      <MediaImage
        hash={undefined}
        title="Andor"
        hueKey="stable-key"
        fallback="monogram"
      />,
    );
    const mono = screen.getByTestId('monogram-fallback') as HTMLElement;
    // The MonogramFallback renders the first letter of `title` —
    // confirms the monogram path was selected (rather than svg).
    expect(mono.textContent).toContain('A');
  });

  it('encodes the hash safely', () => {
    render(
      <MediaImage
        hash="abc/def"
        title="t"
        fallback="monogram"
      />,
    );
    const img = screen.getByTestId('media-image-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/v1/media/abc%2Fdef');
  });

  it('applies custom aspectRatio class', () => {
    render(
      <MediaImage
        hash="abc"
        title="t"
        fallback="monogram"
        aspectRatio="aspect-auto"
        data-testid="custom-wrap"
      />,
    );
    const wrap = screen.getByTestId('custom-wrap');
    expect(wrap.className).toContain('aspect-auto');
  });
});
