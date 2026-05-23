import { DtoDecisionCategory } from '@/api/schema';
import type { BadgeKind } from './badge-variants';

export type CategoryKind =
  | 'all_complete'
  | 'sonarr_handles'
  | 'action_taken'
  | 'blocked'
  | 'nothing_found'
  | 'error'
  | 'unknown';

export interface CategoryDescriptor {
  label: string;
  // BadgeKind drives chip + dot classes via existing KIND_CLASS / KIND_DOT
  // maps — single source of truth, no parallel palette to drift.
  kind: BadgeKind;
  // Extra background-opacity tweak so categories sharing the same kind
  // (blocked, nothing_found both = warning) read as visually distinct.
  bgOpacityClass?: string;
  priority: number;
}

// Higher priority = "more interesting to an operator scanning the list".
export const CATEGORY: Record<CategoryKind, CategoryDescriptor> = {
  action_taken:   { label: 'Action taken',   kind: 'info',    priority: 5 },
  error:          { label: 'Error',          kind: 'danger',  priority: 4 },
  blocked:        { label: 'Blocked',        kind: 'warning', priority: 3 },
  nothing_found:  { label: 'Nothing found',  kind: 'warning', bgOpacityClass: 'bg-status-warning/8', priority: 2 },
  sonarr_handles: { label: 'Sonarr handles', kind: 'neutral', priority: 1 },
  all_complete:   { label: 'All complete',   kind: 'success', priority: 0 },
  unknown:        { label: 'Unknown',        kind: 'neutral', priority: 0 },
};

const KNOWN: ReadonlySet<string> = new Set(
  Object.keys(CATEGORY) as readonly CategoryKind[],
);

// Backend sends DtoDecisionCategory directly; this helper handles missing
// fields (pre-011a rows in the DB) + any future enum value the frontend
// hasn't been rebuilt against. Both fall to 'unknown'.
export function categoryOf(value: string | undefined): CategoryKind {
  if (!value) return 'unknown';
  if (KNOWN.has(value)) return value as CategoryKind;
  return 'unknown';
}

// Re-export the schema enum so callers in tests can use the symbol form.
export { DtoDecisionCategory };
