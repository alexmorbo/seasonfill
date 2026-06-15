import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { CastStrip } from './CastStrip';

function wrap(ui: React.ReactElement) {
  return (
    <MemoryRouter>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </MemoryRouter>
  );
}

const sample = [
  { person_id: 1, name: 'Joel Kinnaman', character_name: 'Ed Baldwin', profile_asset: 'h1' },
  { person_id: 2, name: 'Krys Marshall', character_name: 'Danielle Poole' },
  { person_id: 3, name: 'Wrenn Schmidt', character_name: 'Margo Madison', profile_asset: 'h3' },
];

describe('CastStrip', () => {
  it('returns null when cast is empty', () => {
    const { container } = render(wrap(<CastStrip instance="homelab" seriesId={369} cast={[]} />));
    expect(container.firstChild).toBeNull();
  });

  it('renders one card per cast member up to limit', () => {
    render(wrap(<CastStrip instance="homelab" seriesId={369} cast={sample as unknown as typeof sample} limit={2} />));
    expect(screen.getAllByTestId('cast-strip-card')).toHaveLength(2);
  });

  it('renders the avatar with image when profile_asset is set', () => {
    render(wrap(<CastStrip instance="homelab" seriesId={369} cast={[sample[0]] as unknown as typeof sample} />));
    const av = screen.getByTestId('cast-strip-avatar');
    expect(av.querySelector('img')).toBeTruthy();
  });

  it('renders the monogram fallback when profile_asset is missing', () => {
    render(wrap(<CastStrip instance="homelab" seriesId={369} cast={[sample[1]] as unknown as typeof sample} />));
    const av = screen.getByTestId('cast-strip-avatar');
    expect(av.querySelector('img')).toBeFalsy();
    // MonogramFallback renders initials or a placeholder
    expect(av.textContent).toBeTruthy();
  });

  it('renders name + character labels', () => {
    render(wrap(<CastStrip instance="homelab" seriesId={369} cast={[sample[0]] as unknown as typeof sample} />));
    expect(screen.getByTestId('cast-strip-name').textContent).toMatch(/Joel/);
    expect(screen.getByTestId('cast-strip-character').textContent).toMatch(/Ed Baldwin/);
  });

  it('view-all link points to the cast subpage', () => {
    render(wrap(<CastStrip instance="homelab" seriesId={369} cast={sample as unknown as typeof sample} />));
    const link = screen.getByTestId('cast-strip-view-all');
    expect(link.getAttribute('href')).toBe('/series/homelab/369/cast');
  });

  it('header uses justify-between and the view-all link is a sibling of the heading', () => {
    const cast = [
      { person_id: 1, name: 'Alex', character_name: 'Alex' },
      { person_id: 2, name: 'Sam', character_name: 'Sam' },
    ];
    render(wrap(<CastStrip instance="homelab" seriesId={377} cast={cast as any} />));
    const header = screen.getByTestId('cast-strip-header');
    expect(header.className).toContain('justify-between');
    // view-all is a direct child of the header.
    const viewAll = screen.getByTestId('cast-strip-view-all');
    expect(viewAll.parentElement).toBe(header);
    // No flex-1 spacer in the header.
    expect(header.querySelector('.flex-1')).toBeNull();
  });
});
