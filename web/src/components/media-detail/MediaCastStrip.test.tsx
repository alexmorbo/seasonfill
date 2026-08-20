import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import type { CastMember } from '@/api/series';
import { MediaCastStrip } from './MediaCastStrip';

function wrap(ui: React.ReactElement) {
  return (
    <MemoryRouter>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </MemoryRouter>
  );
}

const sample = [
  { person_id: 1, tmdb_person_id: 1001, name: 'Joel Kinnaman', character_name: 'Ed Baldwin', profile_asset: 'h1' },
  { person_id: 2, tmdb_person_id: 1002, name: 'Krys Marshall', character_name: 'Danielle Poole' },
  { person_id: 3, tmdb_person_id: 1003, name: 'Wrenn Schmidt', character_name: 'Margo Madison', profile_asset: 'h3' },
];

describe('MediaCastStrip', () => {
  it('returns null when cast is empty', () => {
    const { container } = render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={[]} />));
    expect(container.firstChild).toBeNull();
  });

  it('renders one card per cast member up to limit', () => {
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={sample as unknown as typeof sample} limit={2} />));
    expect(screen.getAllByTestId('cast-strip-card')).toHaveLength(2);
  });

  it('renders the avatar with image when profile_asset is set', () => {
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={[sample[0]] as unknown as typeof sample} />));
    const av = screen.getByTestId('cast-strip-avatar');
    expect(av.querySelector('img')).toBeTruthy();
  });

  it('renders the monogram fallback when profile_asset is missing', () => {
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={[sample[1]] as unknown as typeof sample} />));
    const av = screen.getByTestId('cast-strip-avatar');
    expect(av.querySelector('img')).toBeFalsy();
    expect(av.textContent).toBeTruthy();
  });

  it('renders name + character labels', () => {
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={[sample[0]] as unknown as typeof sample} />));
    expect(screen.getByTestId('cast-strip-name').textContent).toMatch(/Joel/);
    expect(screen.getByTestId('cast-strip-character').textContent).toMatch(/Ed Baldwin/);
  });

  it('view-all link points to the cast subpage', () => {
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={sample as unknown as typeof sample} />));
    const link = screen.getByTestId('cast-strip-view-all');
    expect(link.getAttribute('href')).toBe('/series/369/cast');
  });

  it('header uses justify-between and the view-all link is a sibling of the heading', () => {
    const cast = [
      { person_id: 1, name: 'Alex', character_name: 'Alex' },
      { person_id: 2, name: 'Sam', character_name: 'Sam' },
    ];
    render(wrap(<MediaCastStrip castHref="/series/377/cast" seriesId={377} cast={cast as unknown as CastMember[]} />));
    const header = screen.getByTestId('cast-strip-header');
    expect(header.className).toContain('justify-between');
    const viewAll = screen.getByTestId('cast-strip-view-all');
    expect(viewAll.parentElement).toBe(header);
    expect(header.querySelector('.flex-1')).toBeNull();
  });

  it('renders a non-link div for cast members without tmdb_person_id (B-45)', () => {
    const cast: CastMember[] = [
      { person_id: 42, name: 'Sonarr Only', character_name: 'Background' } as CastMember,
    ];
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={cast} />));
    const card = screen.getByTestId('cast-strip-card');
    expect(card.tagName).toBe('DIV');
    expect(card.getAttribute('href')).toBeNull();
    expect(card.getAttribute('data-no-link')).toBe('true');
  });

  it('orders the preview by episode_count desc, then slices to limit (W19-5)', () => {
    const cast: CastMember[] = [
      { person_id: 1, tmdb_person_id: 1, name: 'Billed First', character_name: 'A', episode_count: 1 },
      { person_id: 2, tmdb_person_id: 2, name: 'Guest Star', character_name: 'B', episode_count: 3 },
      { person_id: 3, tmdb_person_id: 3, name: 'Main Lead', character_name: 'C', episode_count: 50 },
    ] as unknown as CastMember[];
    render(wrap(<MediaCastStrip castHref="/series/1/cast" seriesId={1} cast={cast} limit={2} />));
    const names = screen.getAllByTestId('cast-strip-name').map((n) => n.textContent);
    expect(names).toEqual(['Main Lead', 'Guest Star']);
  });

  it('sorts members without episode_count to the end (W19-5)', () => {
    const cast: CastMember[] = [
      { person_id: 1, tmdb_person_id: 1, name: 'No Count', character_name: 'A' },
      { person_id: 2, tmdb_person_id: 2, name: 'Counted', character_name: 'B', episode_count: 10 },
    ] as unknown as CastMember[];
    render(wrap(<MediaCastStrip castHref="/series/1/cast" seriesId={1} cast={cast} limit={1} />));
    const names = screen.getAllByTestId('cast-strip-name').map((n) => n.textContent);
    expect(names).toEqual(['Counted']);
  });

  it('links to /person/${tmdb_person_id} when TMDB id present (B-45)', () => {
    const cast: CastMember[] = [
      { person_id: 42, tmdb_person_id: 12345, name: 'Test Actor', character_name: 'Role' } as CastMember,
    ];
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={cast} />));
    const card = screen.getByTestId('cast-strip-card');
    expect(card.tagName).toBe('A');
    expect(card.getAttribute('href')).toBe('/person/12345');
    expect(card.getAttribute('href')).not.toContain('/people/');
    expect(card.getAttribute('href')).not.toContain('/42');
  });

  it('renders the degraded skeleton with data-series-id when tmdbPersonDegraded and cast is empty', () => {
    render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={[]} tmdbPersonDegraded />));
    const loading = screen.getByTestId('cast-strip-loading');
    expect(loading.getAttribute('data-series-id')).toBe('369');
    expect(screen.getByTestId('cast-strip-loading-label')).toBeInTheDocument();
    expect(screen.getAllByTestId('cast-skeleton-avatar')).toHaveLength(8);
  });

  it('returns null (not skeleton) when tmdbPersonDegraded is false and cast is empty', () => {
    const { container } = render(wrap(<MediaCastStrip castHref="/series/369/cast" seriesId={369} cast={[]} tmdbPersonDegraded={false} />));
    expect(container.firstChild).toBeNull();
  });
});
