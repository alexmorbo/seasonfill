import { describe, expect, it, vi } from 'vitest';
import { screen, render, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { GrabRow } from './GrabRow';
import type { Grab } from '@/lib/grabs/chipBuilder';
import { DtoGrabStatus } from '@/api/schema';

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
});
