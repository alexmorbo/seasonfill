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
  error: 'danger',
};

export function statusKind(s?: string): BadgeKind {
  return (s ? STATUS[s] : undefined) ?? 'neutral';
}

export function outcomeKind(o?: string): BadgeKind {
  return (o ? OUTCOME[o] : undefined) ?? 'neutral';
}

// Return the i18n key for an outcome wire value. Caller passes the
// returned key into `t(..., { defaultValue: raw })` so an unknown wire
// value falls back to the raw string rather than rendering the key.
export function outcomeLabelKey(o?: string): string {
  return o ? `outcomes.${o}` : '';
}

// Same shape as outcomeLabelKey for status wire values used by
// scans / grabs / triggers — anything fed through StatusBadge in
// non-outcome mode.
export function statusLabelKey(s?: string): string {
  return s ? `statuses.${s}` : '';
}

export function healthKind(h?: string): BadgeKind {
  switch (h) {
    case 'Bootstrapping':
      // Story 488 (B-14): a fresh instance before the first preflight
      // completes. Neutral pill — operator sees the spinner overlay
      // instead of a colored dot.
      return 'neutral';
    case 'Available':
      return 'success';
    case 'SelfThrottled':
      // Self-throttled is a transient slowdown caused by our own
      // rate-limiter queue — the backend is reachable, the operator
      // just sees degraded latency. Yellow/amber, not red.
      return 'warning';
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
    case 'Bootstrapping':
      return 'health.bootstrapping';
    case 'Available':
      return 'health.available';
    case 'SelfThrottled':
      return 'health.selfThrottled';
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
  neutral: 'bg-status-neutral/15 text-foreground-2 border-status-neutral/30',
};

export const KIND_DOT: Record<BadgeKind, string> = {
  success: 'bg-status-success',
  danger: 'bg-status-danger',
  warning: 'bg-status-warning',
  info: 'bg-status-info',
  neutral: 'bg-status-neutral',
};
