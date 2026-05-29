import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { KIND_CLASS, KIND_DOT } from '@/lib/badge-variants';
import { CATEGORY, categoryLabelKey, categoryOf } from '@/lib/decision-category';

export function CategoryChip({
  value,
  variant = 'compact',
  className,
}: {
  value: string | undefined;
  variant?: 'compact' | 'full';
  className?: string;
}) {
  const { t } = useTranslation();
  const kind = categoryOf(value);
  const desc = CATEGORY[kind];
  const label = t(categoryLabelKey(kind));
  const base =
    variant === 'compact'
      ? 'inline-flex items-center gap-1.5 px-1.5 h-[18px] rounded-full border font-mono text-[10.5px]'
      : 'inline-flex items-center gap-2 px-2 h-[22px] rounded-full border font-mono text-[11px]';
  return (
    <span
      className={cn(
        base,
        KIND_CLASS[desc.kind],
        desc.bgOpacityClass,
        className,
      )}
      data-category={kind}
      aria-label={`Category: ${label}`}
    >
      <span className={cn('inline-block w-1.5 h-1.5 rounded-full', KIND_DOT[desc.kind])} />
      {label}
    </span>
  );
}
