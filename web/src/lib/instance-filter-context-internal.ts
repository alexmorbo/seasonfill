import { createContext, useContext } from 'react';

export type InstanceFilterCtxValue = {
  filter: string | null;
  setFilter: (v: string | null) => void;
};

export const InstanceFilterCtx = createContext<InstanceFilterCtxValue | null>(null);

export function useInstanceFilter(): InstanceFilterCtxValue {
  const ctx = useContext(InstanceFilterCtx);
  if (!ctx) throw new Error('useInstanceFilter must be used inside <InstanceFilterProvider>');
  return ctx;
}
