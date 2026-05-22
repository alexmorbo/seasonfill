import { useEffect, useState } from 'react';
import { onlineManager } from '@tanstack/react-query';

type NetState = 'online' | 'offline' | 'reconnected';

export function NetBanner() {
  const [state, setState] = useState<NetState>(() => onlineManager.isOnline() ? 'online' : 'offline');

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const unsub = onlineManager.subscribe((isOnline) => {
      setState((prev) => {
        if (!isOnline) return 'offline';
        if (prev === 'offline') {
          if (timer) clearTimeout(timer);
          timer = setTimeout(() => setState('online'), 2000);
          return 'reconnected';
        }
        return prev;
      });
    });
    return () => { unsub(); if (timer) clearTimeout(timer); };
  }, []);

  if (state === 'online') return null;
  const base = 'fixed left-1/2 -translate-x-1/2 bottom-5 z-[70] flex items-center gap-3 px-4 py-2.5 rounded-full bg-surface-2 font-mono text-[12px] shadow-xl';
  if (state === 'reconnected') {
    return <div role="status" className={`${base} border border-status-success/40 text-status-success`}>Reconnected</div>;
  }
  return (
    <div role="alert" className={`${base} border border-status-danger/50 text-foreground-2`}>
      <span className="inline-block w-3.5 h-3.5 rounded-full border-2 border-faint border-t-status-danger animate-spin" />
      Connection lost — retrying…
    </div>
  );
}
