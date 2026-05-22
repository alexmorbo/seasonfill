import { cn } from '@/lib/utils';
import { KIND_CLASS, KIND_DOT, outcomeKind, statusKind, type BadgeKind } from '@/lib/badge-variants';

const DOTTED = new Set([
  'running',
  'pending',
  'import_failed',
  'failed',
  'grab_failed',
  'blocked_cooldown',
]);

export function StatusBadge({
  value,
  mode = 'status',
}: {
  value?: string | undefined;
  mode?: 'status' | 'outcome';
}) {
  if (!value) return <span className="text-faint mono text-[12px]">—</span>;
  const kind: BadgeKind = mode === 'outcome' ? outcomeKind(value) : statusKind(value);
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2 h-[22px] rounded-full border font-mono text-[11px]',
        KIND_CLASS[kind],
      )}
    >
      {DOTTED.has(value) && (
        <span className={cn('inline-block w-1.5 h-1.5 rounded-full', KIND_DOT[kind])} />
      )}
      {value}
    </span>
  );
}
