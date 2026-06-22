import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { Avatar } from './Avatar';

describe('<Avatar />', () => {
  it('renders a gravatar <img> with the canonical URL and size×2 query', () => {
    render(
      <Avatar
        avatar_resolved_mode="gravatar"
        avatar_hash="0bc83cb571cd1c50ba6f3e8a78ef1346"
        username="admin"
        size={64}
      />,
    );
    const img = screen.getByRole('img', { name: /admin/i }) as HTMLImageElement;
    expect(img.src).toBe(
      'https://gravatar.com/avatar/0bc83cb571cd1c50ba6f3e8a78ef1346?s=128&d=404',
    );
    expect(img.width).toBe(64);
    expect(img.height).toBe(64);
  });

  it('uses size * 2 in the gravatar query for retina', () => {
    render(
      <Avatar
        avatar_resolved_mode="gravatar"
        avatar_hash="abc"
        username="u"
        size={96}
      />,
    );
    const img = screen.getByRole('img') as HTMLImageElement;
    expect(img.src).toContain('?s=192&');
  });

  it('falls back to MonogramFallback when the gravatar img fires onError', () => {
    render(
      <Avatar
        avatar_resolved_mode="gravatar"
        avatar_hash="abc"
        username="admin"
      />,
    );
    const img = screen.getByRole('img', { name: /admin/i });
    fireEvent.error(img);
    // After fallback the role="img" element is the monogram (with
    // aria-label "No image — admin").
    expect(screen.getByLabelText(/no image — admin/i)).toBeInTheDocument();
    expect(screen.getByTestId('avatar')).toHaveAttribute(
      'data-resolved-mode',
      'monogram',
    );
  });

  it('renders MonogramFallback directly when mode=monogram (no img)', () => {
    render(
      <Avatar
        avatar_resolved_mode="monogram"
        avatar_hash=""
        username="bob"
      />,
    );
    expect(screen.queryByRole('img', { name: /Avatar of/i })).not.toBeInTheDocument();
    expect(screen.getByLabelText(/no image — bob/i)).toBeInTheDocument();
  });

  it('falls back to monogram when avatar_hash is empty even in gravatar mode', () => {
    render(
      <Avatar
        avatar_resolved_mode="gravatar"
        avatar_hash=""
        username="noemail"
      />,
    );
    expect(screen.queryByRole('img', { name: /Avatar of/i })).not.toBeInTheDocument();
    expect(screen.getByLabelText(/no image — noemail/i)).toBeInTheDocument();
  });

  it('honours custom size on the wrapper', () => {
    render(
      <Avatar
        avatar_resolved_mode="monogram"
        avatar_hash=""
        username="u"
        size={96}
      />,
    );
    const wrap = screen.getByTestId('avatar');
    expect(wrap.style.width).toBe('96px');
    expect(wrap.style.height).toBe('96px');
  });
});
