import { useMemo, useState, type ReactNode } from 'react';
import { InstanceFilterCtx } from './instance-filter-context-internal';

export function InstanceFilterProvider({ children }: { children: ReactNode }) {
  const [filter, setFilter] = useState<string | null>(null);
  const value = useMemo(() => ({ filter, setFilter }), [filter]);
  return <InstanceFilterCtx.Provider value={value}>{children}</InstanceFilterCtx.Provider>;
}
