import { onlineManager } from '@tanstack/react-query';
import { toast } from 'sonner';

export type DebugActions = { simulate401: () => void; simulateNetLoss: () => void };

export function useDebugActions(): DebugActions {
  return {
    simulate401: () => {
      toast.error('401 simulated', { description: 'Forcing /login redirect' });
      window.location.assign('/login?next=' + encodeURIComponent(window.location.pathname));
    },
    simulateNetLoss: () => {
      onlineManager.setOnline(false);
      toast.warning('Net loss simulated', { description: 'Auto-recovers in 8s' });
      setTimeout(() => { onlineManager.setOnline(true); toast.success('Reconnected'); }, 8000);
    },
  };
}
