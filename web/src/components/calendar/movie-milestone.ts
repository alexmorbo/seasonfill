import { Clapperboard, MonitorPlay, Disc } from 'lucide-react';
import type { MovieCalendarEvent } from '@/api/movieCalendar';

// ── movie milestone presentation ─────────────────────────────────────────
// The movie calendar has three milestones: theatrical (cinema release),
// digital (VOD/streaming) and physical (disc). Kept in a plain module (not the
// component file) so the agenda row and the month-grid chip can share it
// without tripping react-refresh/only-export-components.

export type MovieMilestoneMeta = {
  Icon: typeof Clapperboard;
  labelKey: string;
  className: string;
};

export function movieMilestoneMeta(m?: string | null): MovieMilestoneMeta | null {
  if (m === 'theatrical')
    return { Icon: Clapperboard, labelKey: 'calendar.milestone.theatrical', className: 'text-accent' };
  if (m === 'digital')
    return { Icon: MonitorPlay, labelKey: 'calendar.milestone.digital', className: 'text-info' };
  if (m === 'physical')
    return { Icon: Disc, labelKey: 'calendar.milestone.physical', className: 'text-ok' };
  return null;
}

export function movieMilestoneEmoji(m?: string | null): string {
  if (m === 'theatrical') return '🎬';
  if (m === 'digital') return '🖥️';
  if (m === 'physical') return '💿';
  return '';
}

export function movieEventTestId(e: MovieCalendarEvent): string {
  return `calendar-movie-event-${e.tmdb_id ?? 0}`;
}
