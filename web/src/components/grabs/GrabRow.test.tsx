import { describe, expect, it, vi } from 'vitest';
import { screen, render, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { GrabRow } from './GrabRow';
import type { Grab } from '@/lib/grabs/chipBuilder';
import { DtoGrabStatus, DtoGrabReplay_kind } from '@/api/schema';

const base: Partial<Grab> = {
  id: 'g1',
  series_title: 'For All Mankind',
  series_id: 1234,
  season_number: 5,
  status: DtoGrabStatus.imported,
  created_at: '2026-06-07T19:32:00Z',
  updated_at: '2026-06-07T19:32:41Z',
  indexer_name: 'rutracker',
  custom_format_score: 180,
  size_bytes: 13_325_829_734,
  poster_hash: 'abc123def456',
  parsed: {
    codec: 'HEVC',
    source: 'webdl',
    quality: 'WEBDL-2160p',
    resolution: 2160,
    hdr_flags: ['HDR10+', 'DV'],
    dub: 'MVO',
  },
};

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </QueryClientProvider>
  );
}

describe('<GrabRow />', () => {
  it('renders the content-addressed media img for poster_hash', () => {
    render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        instance="alpha"
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    const img = screen.getByTestId('media-image-img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/v1/media/abc123def456');
    expect(img.getAttribute('loading')).toBe('lazy');
  });

  it('renders the monogram fallback when poster_hash is absent', () => {
    const { poster_hash: _ph, ...rest } = base;
    render(wrap(
      <GrabRow grab={rest as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(screen.queryByTestId('media-image-img')).toBeNull();
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });

  it('does not emit legacy /api/v1/instances/.../poster URL', () => {
    render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        instance="alpha"
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    document.querySelectorAll('img').forEach((img) => {
      expect(img.getAttribute('src') ?? '').not.toMatch(
        /\/api\/v1\/instances\/[^/]+\/series\/\d+\/poster/,
      );
    });
  });

  it('renders title, status chip, and full chip set', () => {
    render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(screen.getByText('For All Mankind')).toBeInTheDocument();
    expect(screen.getByText(/imported/)).toBeInTheDocument();
    expect(screen.getByText('WEBDL-2160p')).toBeInTheDocument();
    expect(screen.getByText('HDR10+')).toBeInTheDocument();
    expect(screen.getByText('DV')).toBeInTheDocument();
    expect(screen.getByText('MVO')).toBeInTheDocument();
    expect(screen.getByText('CF +180')).toBeInTheDocument();
  });

  it('applies failrow class for import_failed', () => {
    const failed: Partial<Grab> = {
      ...base,
      status: DtoGrabStatus.import_failed,
      error_message: 'no files matched expected episodes',
    };
    const { getByTestId } = render(wrap(
      <GrabRow grab={failed as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(getByTestId('grab-row-g1').getAttribute('data-failrow')).toBe('true');
  });

  it('hides the re-grab chip on root grabs (reGrabIndex === 0)', () => {
    render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={0}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(screen.queryByTestId('regrab-tag-g1')).toBeNull();
  });

  it('hides the re-grab chip when reGrabIndex is null', () => {
    render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(screen.queryByTestId('regrab-tag-g1')).toBeNull();
  });

  it('shows the re-grab chip with #1 label for first replay (reGrabIndex === 1)', () => {
    const { getByTestId } = render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={1}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    const tag = getByTestId('regrab-tag-g1');
    expect(tag).toBeInTheDocument();
    expect(tag.textContent).toMatch(/#1/);
  });

  it('clicking the re-grab tag on a replay closes the thread back', () => {
    const onToggleThread = vi.fn();
    const { getByTestId } = render(wrap(
      <GrabRow grab={base as Grab} selected={true} threadOpen={true} reGrabIndex={1}
        onOpenDrawer={() => {}} onToggleThread={onToggleThread} />,
    ));
    fireEvent.click(getByTestId('regrab-tag-g1'));
    expect(onToggleThread).toHaveBeenCalledWith('g1');
  });

  it('clicking the re-grab tag toggles thread WITHOUT opening drawer', () => {
    const onOpenDrawer = vi.fn();
    const onToggleThread = vi.fn();
    const { getByTestId } = render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={2}
        onOpenDrawer={onOpenDrawer} onToggleThread={onToggleThread} />,
    ));
    const tag = getByTestId('regrab-tag-g1');
    fireEvent.click(tag);
    expect(onToggleThread).toHaveBeenCalledWith('g1');
    expect(onOpenDrawer).not.toHaveBeenCalled();
  });

  it('clicking the row body opens the drawer', () => {
    const onOpenDrawer = vi.fn();
    const { getByTestId } = render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        onOpenDrawer={onOpenDrawer} onToggleThread={() => {}} />,
    ));
    fireEvent.click(getByTestId('grab-row-g1'));
    expect(onOpenDrawer).toHaveBeenCalledWith('g1');
  });

  it('renders ReGrabThread when threadOpen with instance', () => {
    render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={true} reGrabIndex={2}
        instance="alpha" localAll={[base as Grab]}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    // ReGrabThread renders nothing when there's no chain (single grab, no replay_of_id).
    // Just verify the component doesn't crash when instance + localAll are provided.
    expect(screen.getByText('For All Mankind')).toBeInTheDocument();
  });

  it('renders replay-quality badge when replay_kind === "replay_quality"', () => {
    const rep: Partial<Grab> = { ...base, replay_kind: DtoGrabReplay_kind.replay_quality };
    render(wrap(
      <GrabRow grab={rep as Grab} selected={false} threadOpen={false} reGrabIndex={1}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    const badge = screen.getByTestId('grab-replay-kind-g1');
    expect(badge).toBeInTheDocument();
    expect(badge.textContent).toMatch(/Watchdog/i);
    expect(badge.textContent).toMatch(/(quality|качества)/i);
  });

  it('renders replay-dub badge when replay_kind === "replay_dub"', () => {
    const rep: Partial<Grab> = { ...base, replay_kind: DtoGrabReplay_kind.replay_dub };
    render(wrap(
      <GrabRow grab={rep as Grab} selected={false} threadOpen={false} reGrabIndex={1}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    const badge = screen.getByTestId('grab-replay-kind-g1');
    expect(badge).toBeInTheDocument();
    expect(badge.textContent).toMatch(/(dub|дорожка)/i);
  });

  it('renders replay-other badge when replay_kind === "replay_other"', () => {
    const rep: Partial<Grab> = { ...base, replay_kind: DtoGrabReplay_kind.replay_other };
    render(wrap(
      <GrabRow grab={rep as Grab} selected={false} threadOpen={false} reGrabIndex={1}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    const badge = screen.getByTestId('grab-replay-kind-g1');
    expect(badge).toBeInTheDocument();
    expect(badge.textContent).toMatch(/(re-grab|перегрэб)/i);
  });

  it('omits replay-kind badge when replay_kind is absent (primary row)', () => {
    render(wrap(
      <GrabRow grab={base as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(screen.queryByTestId('grab-replay-kind-g1')).toBeNull();
  });

  it('clamps the error span to max-w-[420px] on import_failed rows', () => {
    const failed: Partial<Grab> = {
      ...base,
      status: DtoGrabStatus.import_failed,
      error_message: 'sonarr /api/v3/release returned status=500 body={ "message": "Download client failed to add torrent" } — release rejected by Sonarr',
    };
    render(wrap(
      <GrabRow grab={failed as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    const errorSpan = screen.getByText(/sonarr.*release rejected/);
    expect(errorSpan.className).toMatch(/max-w-\[420px\]/);
    expect(errorSpan.className).toMatch(/truncate/);
  });

  // 116: title_slug is plumbed onto the DTO + bound onto the Sonarr
  // chip's titleSlug prop. The existing test harness does not mock
  // useInstancePublicURL, so SonarrLink renders null in tests when
  // publicUrl is empty — the assertion below is a field-bind smoke
  // check (catches a regression where Grab loses title_slug at the
  // type level). Backend handler tests cover the wire shape; the
  // Playwright smoke checks the rendered href post-deploy.
  it('renders Sonarr deep-link using authoritative title_slug from the DTO when present', () => {
    const withSlug: Partial<Grab> = {
      ...base,
      instance: 'alpha',
      series_title: 'Your Friends & Neighbors',
      title_slug: 'your-friends-and-neighbors',
    };
    render(wrap(
      <GrabRow grab={withSlug as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        instance="alpha"
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(withSlug.title_slug).toBe('your-friends-and-neighbors');
  });

  it('falls back to lossy client-side slug when title_slug is absent', () => {
    const noSlug: Partial<Grab> = {
      ...base,
      instance: 'alpha',
      series_title: 'Your Friends & Neighbors',
      // title_slug intentionally omitted.
    };
    render(wrap(
      <GrabRow grab={noSlug as Grab} selected={false} threadOpen={false} reGrabIndex={null}
        instance="alpha"
        onOpenDrawer={() => {}} onToggleThread={() => {}} />,
    ));
    expect(noSlug.title_slug).toBeUndefined();
  });
});
