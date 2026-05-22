import { type ReactNode } from 'react';
import { SearchX } from 'lucide-react';

export function EmptyState({
  icon,
  title,
  body,
  action,
}: {
  icon?: ReactNode | undefined;
  title: string;
  body?: ReactNode | undefined;
  action?: ReactNode | undefined;
}) {
  return (
    <div className="flex flex-col items-center justify-center text-center px-6 py-12 gap-2">
      <div className="text-muted mb-1">{icon ?? <SearchX className="w-7 h-7" />}</div>
      <h3 className="text-[15px] font-semibold tracking-tight">{title}</h3>
      {body && <p className="text-[13px] text-muted max-w-sm">{body}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
