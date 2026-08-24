import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { CollectionCard } from './CollectionCard';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider delayDuration={0}>
        <MemoryRouter>{node}</MemoryRouter>
      </TooltipProvider>
    </I18nextProvider>,
  );
}

describe('<CollectionCard />', () => {
  it('renders the collection name', () => {
    r(<CollectionCard tmdbId={10} name="The Matrix Collection" />);
    expect(screen.getByTestId('collection-card-name')).toHaveTextContent(
      'The Matrix Collection',
    );
  });

  it('links to /collections/:tmdbId', () => {
    r(<CollectionCard tmdbId={10} name="The Matrix Collection" />);
    const card = screen.getByTestId('collection-card');
    expect(card.tagName.toLowerCase()).toBe('a');
    expect(card.getAttribute('href')).toBe('/collections/10');
  });

  it('resolves the poster via /api/v1/media/<hash> WITHOUT double-encoding', () => {
    r(<CollectionCard tmdbId={10} name="Matrix" poster="collposterhash" />);
    const img = screen.getByTestId('media-image-img') as HTMLImageElement;
    const src = img.getAttribute('src') ?? '';
    expect(src).toBe('/api/v1/media/collposterhash');
    expect(src).not.toContain('%2Fapi%2Fv1%2Fmedia');
  });

  it('renders a monogram fallback and no poster img when poster is absent', () => {
    r(<CollectionCard tmdbId={10} name="Nameless" />);
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
    expect(screen.queryByTestId('media-image-img')).toBeNull();
  });
});
