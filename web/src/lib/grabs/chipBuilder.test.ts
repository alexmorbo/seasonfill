import { describe, expect, it } from 'vitest';
import { buildChips, CHIP_CLASS } from './chipBuilder';
import type { Grab } from './chipBuilder';
import { DtoGrabStatus } from '@/api/schema';

const base: Partial<Grab> = {
  id: 'g1',
  series_title: 'For All Mankind',
  season_number: 5,
  status: DtoGrabStatus.imported,
};

function fixture(over: Partial<Grab> = {}): Grab {
  return { ...base, ...over } as Grab;
}

describe('buildChips', () => {
  it('full stack: WEBDL-2160p + HDR10+ + DV + HEVC + WEB-DL + MVO + 12.4 GB + CF +180', () => {
    const g = fixture({
      custom_format_score: 180,
      size_bytes: 13_325_829_734,
      parsed: {
        codec: 'HEVC', source: 'webdl', quality: 'WEBDL-2160p',
        resolution: 2160, hdr_flags: ['HDR10+', 'DV'], dub: 'MVO',
        languages: ['Russian'], subs: [], release_group: 'NTb',
      },
    } as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S05 · E1–10' });
    const labels = chips.map((c) => c.label);
    expect(labels).toEqual([
      'S05 · E1–10',
      'WEBDL-2160p',
      'HDR10+',
      'DV',
      'HEVC',
      'WEB-DL',
      'MVO',
      expect.stringMatching(/12\.4 GB/),
      'CF +180',
    ]);
    expect(chips.find((c) => c.label === 'WEBDL-2160p')!.variant).toBe('q2160');
  });

  it('1080p uses q1080 hue', () => {
    const g = fixture({
      parsed: { resolution: 1080, quality: 'WEBDL-1080p' },
    } as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S02 · E10' });
    expect(chips.find((c) => c.label === 'WEBDL-1080p')!.variant).toBe('q1080');
  });

  it('720p uses muted q720 hue', () => {
    const g = fixture({
      parsed: { resolution: 720, quality: 'WEBDL-720p' },
    } as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01 · E1' });
    expect(chips.find((c) => c.variant === 'q720')).toBeDefined();
  });

  it('parsed null → only eps chip (no quality / hdr / dub)', () => {
    const g = fixture({ parsed: null } as unknown as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.map((c) => c.label)).toEqual(['S01']);
  });

  it('multiple HDR flags → multiple chips', () => {
    const g = fixture({
      parsed: { hdr_flags: ['HDR10', 'HDR10+', 'DV'] },
    } as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.filter((c) => c.variant === 'hdr').map((c) => c.label)).toEqual([
      'HDR10', 'HDR10+', 'DV',
    ]);
  });

  it('dub=Multi → accent chip', () => {
    const g = fixture({ parsed: { dub: 'Multi' } } as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.find((c) => c.label === 'Multi')!.variant).toBe('dub');
  });

  it('cf score negative → cf-neg chip', () => {
    const g = fixture({ custom_format_score: -20 });
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.find((c) => c.variant === 'cf-neg')!.label).toBe('CF −20');
  });

  it('cf score positive → cf-pos chip', () => {
    const g = fixture({ custom_format_score: 90 });
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.find((c) => c.variant === 'cf-pos')!.label).toBe('CF +90');
  });

  it('cf score = 0 → no CF chip', () => {
    const g = fixture({ custom_format_score: 0 });
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.find((c) => c.id === 'cf')).toBeUndefined();
  });

  it('missing size_bytes → no size chip', () => {
    const g = fixture({ size_bytes: null } as unknown as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.find((c) => c.id === 'size')).toBeUndefined();
  });

  it('source normalisation: webdl → WEB-DL', () => {
    const g = fixture({ parsed: { source: 'webdl' } } as Partial<Grab>);
    const chips = buildChips({ grab: g, episodeRangeLabel: 'S01' });
    expect(chips.find((c) => c.id === 'source')!.label).toBe('WEB-DL');
  });

  it('CHIP_CLASS has an entry for every variant', () => {
    const variants: Array<keyof typeof CHIP_CLASS> = [
      'eps', 'q2160', 'q1080', 'q720', 'qOther', 'hdr', 'dub',
      'cf-pos', 'cf-neg', 'muted', 'solid',
    ];
    for (const v of variants) expect(CHIP_CLASS[v]).toBeTruthy();
  });
});
