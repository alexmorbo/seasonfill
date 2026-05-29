import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import {
  KIND_CLASS,
  KIND_DOT,
  healthKind,
  healthLabelKey,
  outcomeKind,
  outcomeLabelKey,
  statusKind,
  statusLabelKey,
  type BadgeKind,
} from '@/lib/badge-variants';

const DOTTED = new Set([
  'running',
  'pending',
  'import_failed',
  'failed',
  'grab_failed',
  'blocked_cooldown',
  'error',
]);

export function StatusBadge({
  value,
  mode = 'status',
}: {
  value?: string | undefined;
  mode?: 'status' | 'outcome' | 'health';
}) {
  const { t } = useTranslation();
  if (!value) return <span className="text-faint mono text-[12px]">—</span>;

  if (mode === 'health') {
    const kind = healthKind(value);
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1.5 px-2 h-[22px] rounded-full border font-mono text-[11px]',
          KIND_CLASS[kind],
        )}
      >
        <span className={cn('inline-block w-1.5 h-1.5 rounded-full', KIND_DOT[kind])} />
        {t(healthLabelKey(value))}
      </span>
    );
  }

  const kind: BadgeKind = mode === 'outcome' ? outcomeKind(value) : statusKind(value);
  const labelKey = mode === 'outcome' ? outcomeLabelKey(value) : statusLabelKey(value);
  const label = t(labelKey, { defaultValue: value });
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
      {label}
    </span>
  );
}
