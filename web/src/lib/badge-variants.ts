export type BadgeKind = 'success' | 'danger' | 'warning' | 'info' | 'neutral';

const STATUS: Record<string, BadgeKind> = {
  grabbed: 'info',
  imported: 'success',
  import_failed: 'danger',
  grab_failed: 'danger',
  expired: 'neutral',
  failed: 'danger',
  running: 'info',
  completed: 'success',
  pending: 'warning',
  aborted: 'warning',
  // 012: user-initiated cancel. Same warning kind as aborted; the
  // literal text differentiates in the badge.
  cancelled: 'warning',
};

const OUTCOME: Record<string, BadgeKind> = {
  grab: 'success',
  force_grab: 'success',
  skip: 'neutral',
  already_optimal: 'neutral',
  blocked_cooldown: 'danger',
  expired: 'neutral',
};

export function statusKind(s?: string): BadgeKind {
  return (s ? STATUS[s] : undefined) ?? 'neutral';
}

export function outcomeKind(o?: string): BadgeKind {
  return (o ? OUTCOME[o] : undefined) ?? 'neutral';
}

export function healthKind(h?: string): BadgeKind {
  switch (h) {
    case 'Available':
      return 'success';
    case 'UnavailableAuth':
    case 'UnavailableNetwork':
    case 'UnavailableUnknown':
      return 'danger';
    default:
      return 'neutral';
  }
}

export function healthLabelKey(h?: string): string {
  switch (h) {
    case 'Available':
      return 'health.available';
    case 'UnavailableAuth':
      return 'health.unavailableAuth';
    case 'UnavailableNetwork':
      return 'health.unavailableNetwork';
    case 'UnavailableUnknown':
      return 'health.unavailableUnknown';
    default:
      return 'health.unknown';
  }
}

export const KIND_CLASS: Record<BadgeKind, string> = {
  success: 'bg-status-success/15 text-status-success border-status-success/30',
  danger: 'bg-status-danger/15 text-status-danger border-status-danger/30',
  warning: 'bg-status-warning/15 text-status-warning border-status-warning/30',
  info: 'bg-status-info/15 text-status-info border-status-info/30',
  neutral: 'bg-surface-2 text-foreground-2 border-border-faint',
};

export const KIND_DOT: Record<BadgeKind, string> = {
  success: 'bg-status-success',
  danger: 'bg-status-danger',
  warning: 'bg-status-warning',
  info: 'bg-status-info',
  neutral: 'bg-status-neutral',
};
