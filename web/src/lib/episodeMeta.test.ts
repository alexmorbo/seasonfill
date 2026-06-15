import { describe, it, expect } from 'vitest';
import { formatEpisodeMeta } from './episodeMeta';

describe('formatEpisodeMeta', () => {
  it('returns empty string for an episode without a file', () => {
    expect(formatEpisodeMeta({ has_file: false } as any)).toBe('');
  });

  it('joins all four parts with mid-dots', () => {
    const got = formatEpisodeMeta({
      has_file: true,
      quality: 'WEB-DL 1080p',
      video_codec: 'HEVC',
      audio_codec: 'DDP',
      audio_channels: '5.1',
      release_group: 'RARBG',
    } as any);
    expect(got).toBe('WEB-DL 1080p · HEVC · DD+ 5.1 · RARBG');
  });

  it('skips missing parts cleanly', () => {
    expect(formatEpisodeMeta({
      has_file: true,
      quality: 'WEB-DL 1080p',
      video_codec: 'HEVC',
    } as any)).toBe('WEB-DL 1080p · HEVC');
  });

  it('combines audio_codec + audio_channels into a single segment', () => {
    expect(formatEpisodeMeta({
      has_file: true, audio_codec: 'DDP', audio_channels: '5.1',
    } as any)).toBe('DD+ 5.1');
  });

  it('renders audio_codec alone when channels missing', () => {
    expect(formatEpisodeMeta({
      has_file: true, audio_codec: 'DTS',
    } as any)).toBe('DTS');
  });

  it('renders audio_channels alone when codec missing', () => {
    expect(formatEpisodeMeta({
      has_file: true, audio_channels: '5.1',
    } as any)).toBe('5.1');
  });

  it('normalises EAC3 / DDPLUS to DD+', () => {
    expect(formatEpisodeMeta({
      has_file: true, audio_codec: 'EAC3', audio_channels: '2.0',
    } as any)).toBe('DD+ 2.0');
  });

  it('returns empty when has_file=true but no meta fields', () => {
    expect(formatEpisodeMeta({ has_file: true } as any)).toBe('');
  });
});
