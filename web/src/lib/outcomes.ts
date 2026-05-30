export const OUTCOMES = [
  'grab',
  'skip',
  'already_optimal',
  'blocked_cooldown',
  'expired',
  'error',
] as const;
export type Outcome = (typeof OUTCOMES)[number];
