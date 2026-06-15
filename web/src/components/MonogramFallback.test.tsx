import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MonogramFallback } from './MonogramFallback';

describe('<MonogramFallback />', () => {
  it('renders the engraved sf glyph as an accessible image', () => {
    render(<MonogramFallback title="For All Mankind" />);
    const node = screen.getByTestId('monogram-fallback');
    expect(node.getAttribute('role')).toBe('img');
    expect(node.getAttribute('aria-label')).toBe('No image — For All Mankind');
    // sf glyph rendered as `s<b>f</b>` — combined textContent is "sf"
    expect(node.textContent).toBe('sf');
  });

  it('defaults to kind=poster (108px glyph)', () => {
    render(<MonogramFallback title="T" />);
    const node = screen.getByTestId('monogram-fallback');
    expect(node.getAttribute('data-kind')).toBe('poster');
    const glyph = node.querySelector('.glyph') as HTMLElement;
    expect(glyph.style.getPropertyValue('--gs')).toBe('108px');
  });

  it('renders kind=backdrop with the larger glyph size', () => {
    render(<MonogramFallback title="T" kind="backdrop" />);
    const node = screen.getByTestId('monogram-fallback');
    expect(node.getAttribute('data-kind')).toBe('backdrop');
    const glyph = node.querySelector('.glyph') as HTMLElement;
    expect(glyph.style.getPropertyValue('--gs')).toBe('230px');
    // No round clip on backdrop.
    expect(node.className).not.toContain('ph--avatar');
  });

  it('renders kind=avatar with the round-clip modifier and 86px glyph', () => {
    render(<MonogramFallback title="T" kind="avatar" />);
    const node = screen.getByTestId('monogram-fallback');
    expect(node.getAttribute('data-kind')).toBe('avatar');
    expect(node.className).toContain('ph--avatar');
    const glyph = node.querySelector('.glyph') as HTMLElement;
    expect(glyph.style.getPropertyValue('--gs')).toBe('86px');
  });

  it('ignores hueKey (color is brand-fixed)', () => {
    render(<MonogramFallback title="T" hueKey="any-string" />);
    // No assertion on color — just that the component does not throw
    // when given hueKey for backwards compatibility with MediaImage.
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });
});
