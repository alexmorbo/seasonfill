import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { TrailerModal } from './TrailerModal';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>{node}</I18nextProvider>,
  );
}

describe('<TrailerModal />', () => {
  it('renders nothing when closed', () => {
    r(<TrailerModal open={false} onOpenChange={() => {}} youtubeKey="abc123" />);
    expect(screen.queryByTestId('trailer-modal-iframe')).not.toBeInTheDocument();
  });

  it('mounts the iframe with the privacy-respecting embed URL when open', () => {
    r(<TrailerModal open onOpenChange={() => {}} youtubeKey="abc123" name="Season 5 Trailer" />);
    const iframe = screen.getByTestId('trailer-modal-iframe') as HTMLIFrameElement;
    expect(iframe.src).toContain('youtube-nocookie.com/embed/abc123');
    expect(iframe.src).toContain('autoplay=1');
    expect(iframe.src).toContain('rel=0');
    expect(iframe.title).toBe('Season 5 Trailer');
  });

  it('uses the i18n fallback title when name is missing', () => {
    r(<TrailerModal open onOpenChange={() => {}} youtubeKey="abc123" />);
    // Resolves to either "Trailer" or "Трейлер" depending on test locale —
    // assert via the testid + non-empty text rule.
    const title = screen.getByTestId('trailer-modal-title');
    expect(title.textContent ?? '').not.toBe('');
  });

  it('fires onOpenChange(false) when Escape is pressed', async () => {
    const onOpenChange = vi.fn();
    r(<TrailerModal open onOpenChange={onOpenChange} youtubeKey="abc123" />);
    await userEvent.keyboard('{Escape}');
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('encodes the youtube key into the embed URL', () => {
    // Keys are always 11 chars alnum/dash/underscore in practice; we
    // still encode for defensive correctness.
    r(<TrailerModal open onOpenChange={() => {}} youtubeKey="abc/123" />);
    const iframe = screen.getByTestId('trailer-modal-iframe') as HTMLIFrameElement;
    expect(iframe.src).toContain('abc%2F123');
  });
});
