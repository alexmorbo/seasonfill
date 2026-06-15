import type { components } from '@/api/schema';

type Episode = components['schemas']['dto.Episode'];

const AUDIO_CODEC_DISPLAY: Record<string, string> = {
  DDP: 'DD+',
  DDPLUS: 'DD+',
  EAC3: 'DD+',
  AC3: 'DD',
  DD: 'DD',
};

function normalizeAudioCodec(raw: string | undefined): string {
  if (!raw) return '';
  const key = raw.trim().toUpperCase();
  return AUDIO_CODEC_DISPLAY[key] ?? raw;
}

/**
 * Build the right-aligned `.eq` chip line for an episode row.
 * Returns an empty string when the episode has no file or no
 * media-meta fields populated — caller suppresses the element.
 */
export function formatEpisodeMeta(ep: Episode | undefined): string {
  if (!ep || !ep.has_file) return '';
  const parts: string[] = [];

  if (ep.quality && ep.quality.trim()) parts.push(ep.quality.trim());
  if (ep.video_codec && ep.video_codec.trim()) parts.push(ep.video_codec.trim());

  const audioCodec = normalizeAudioCodec(ep.audio_codec);
  const audioCh = (ep.audio_channels ?? '').trim();
  if (audioCodec && audioCh) parts.push(`${audioCodec} ${audioCh}`);
  else if (audioCodec) parts.push(audioCodec);
  else if (audioCh) parts.push(audioCh);

  if (ep.release_group && ep.release_group.trim()) parts.push(ep.release_group.trim());

  return parts.join(' · ');
}
