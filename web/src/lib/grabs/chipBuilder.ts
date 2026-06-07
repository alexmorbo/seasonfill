import type { components } from '@/api/schema';
import { formatSize } from './format';

export type Grab = components['schemas']['dto.Grab'];

export type ChipVariant =
  | 'eps'
  | 'q2160'
  | 'q1080'
  | 'q720'
  | 'qOther'
  | 'hdr'
  | 'dub'
  | 'cf-pos'
  | 'cf-neg'
  | 'muted'
  | 'solid';

export interface Chip {
  readonly id: string;       // stable key for React
  readonly label: string;    // visible text
  readonly variant: ChipVariant;
  readonly title?: string;   // optional tooltip
}

// Maps Sonarr `parsed.resolution` (or quality-name fallback) to the
// design's q2160/q1080/q720 hue chips. The quality chip label uses
// `parsed.quality` (e.g. "WEBDL-2160p"); fallback to "{res}p" then
// "—".
function qualityChip(p: NonNullable<Grab['parsed']>): Chip | null {
  const res = p.resolution ?? 0;
  const label = p.quality ?? (res > 0 ? `${res}p` : '');
  if (!label) return null;
  let variant: ChipVariant = 'qOther';
  if (res >= 2160) variant = 'q2160';
  else if (res >= 1080) variant = 'q1080';
  else if (res >= 720) variant = 'q720';
  return { id: 'q', label, variant };
}

// HDR flags array → multiple gold chips. "HDR10+", "DV", "HDR10", "HLG".
function hdrChips(p: NonNullable<Grab['parsed']>): Chip[] {
  const flags = p.hdr_flags ?? [];
  return flags.map((f, i) => ({ id: `hdr-${i}-${f}`, label: f, variant: 'hdr' as const }));
}

// Dub → accent chip. MVO / DUB / Multi / Original / VO.
function dubChip(p: NonNullable<Grab['parsed']>): Chip | null {
  if (!p.dub) return null;
  return { id: 'dub', label: p.dub, variant: 'dub' };
}

function cfChip(score: number | null | undefined): Chip | null {
  if (score === null || score === undefined || score === 0) return null;
  const sign = score > 0 ? '+' : '−';
  return {
    id: 'cf',
    label: `CF ${sign}${Math.abs(score)}`,
    variant: score > 0 ? 'cf-pos' : 'cf-neg',
  };
}

function sizeChip(bytes: number | null | undefined): Chip | null {
  if (bytes === null || bytes === undefined) return null;
  return { id: 'size', label: formatSize(bytes), variant: 'muted' };
}

export interface BuildChipsArgs {
  grab: Grab;
  episodeRangeLabel: string;       // pre-formatted via format.formatEpisodeRange()
}

export function buildChips({ grab, episodeRangeLabel }: BuildChipsArgs): Chip[] {
  const out: Chip[] = [];
  // 1. Episodes (always shown)
  out.push({ id: 'eps', label: episodeRangeLabel, variant: 'eps' });
  // 2. Parsed chips (quality, HDR, codec, source, dub)
  if (grab.parsed) {
    const q = qualityChip(grab.parsed);
    if (q) out.push(q);
    out.push(...hdrChips(grab.parsed));
    if (grab.parsed.codec) {
      out.push({ id: 'codec', label: grab.parsed.codec, variant: 'solid' });
    }
    if (grab.parsed.source) {
      // Normalise: "webdl" → "WEB-DL", "bluray" → "BluRay", "webrip" → "WEBRip"
      const src = grab.parsed.source.toLowerCase();
      const label =
        src === 'webdl' ? 'WEB-DL'
        : src === 'webrip' ? 'WEBRip'
        : src === 'bluray' ? 'BluRay'
        : grab.parsed.source.toUpperCase();
      out.push({ id: 'source', label, variant: 'solid' });
    }
    const d = dubChip(grab.parsed);
    if (d) out.push(d);
  }
  // 3. Size (right of dub)
  const s = sizeChip(grab.size_bytes ?? null);
  if (s) out.push(s);
  // 4. CF score
  const cf = cfChip(grab.custom_format_score ?? null);
  if (cf) out.push(cf);
  return out;
}

// Token-class map (the only place where chip-class strings live). The
// row/drawer renderer reads chip.variant and looks up the class set.
// All token references go through Tailwind utilities resolving to the
// 042a @theme tokens.
export const CHIP_CLASS: Record<ChipVariant, string> = {
  eps: 'bg-bg-surface-2 text-tx-primary border-border-subtle',
  q2160:
    'text-[oklch(0.78_0.13_300)] border-[oklch(0.70_0.13_300_/_0.4)] bg-[oklch(0.70_0.13_300_/_0.12)]',
  q1080:
    'text-[oklch(0.78_0.12_230)] border-[oklch(0.74_0.13_230_/_0.4)] bg-[oklch(0.74_0.13_230_/_0.12)]',
  q720: 'text-tx-muted border-border-subtle bg-bg-surface-2',
  qOther: 'text-tx-secondary border-border-subtle bg-bg-surface-2',
  hdr: 'text-warn border-warn/35 bg-bg-surface-2',
  dub: 'text-accent border-accent/40 bg-accent-dim',
  'cf-pos': 'text-ok border-ok/35 bg-bg-surface-2',
  'cf-neg': 'text-danger border-danger/40 bg-bg-surface-2',
  muted: 'text-tx-faint bg-transparent border-border-faint',
  solid: 'text-tx-secondary border-border-subtle bg-bg-surface-2',
};
