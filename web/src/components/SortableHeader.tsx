import type { KeyboardEvent, ReactNode } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { SortDir } from '@/lib/use-sort';

interface Props {
  readonly label: ReactNode;
  readonly sortKey: string;
  readonly currentKey: string | null;
  readonly currentDir: SortDir | null;
  readonly onToggle: (key: string) => void;
  readonly className?: string;
}

export function SortableHeader({
  label,
  sortKey,
  currentKey,
  currentDir,
  onToggle,
  className,
}: Props) {
  const active = currentKey === sortKey && currentDir !== null;
  const dir: SortDir | null = active ? currentDir : null;
  const ariaSort: 'ascending' | 'descending' | 'none' =
    dir === 'asc' ? 'ascending' : dir === 'desc' ? 'descending' : 'none';

  const handleKey = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onToggle(sortKey);
    }
  };

  return (
    <button
      type="button"
      aria-sort={ariaSort}
      aria-label={typeof label === 'string' ? label : sortKey}
      onClick={() => onToggle(sortKey)}
      onKeyDown={handleKey}
      data-sort-key={sortKey}
      data-sort-dir={dir ?? ''}
      className={cn(
        'inline-flex items-center gap-1 cursor-pointer select-none',
        'hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded',
        active && 'text-foreground',
        className,
      )}
    >
      <span>{label}</span>
      <span aria-hidden className="w-3 h-3 inline-flex items-center justify-center text-faint">
        {dir === 'asc' ? (
          <ChevronUp className="w-3 h-3" />
        ) : dir === 'desc' ? (
          <ChevronDown className="w-3 h-3" />
        ) : null}
      </span>
    </button>
  );
}
