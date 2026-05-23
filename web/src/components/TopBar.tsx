import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Menu, Settings, LogOut, ShieldAlert, WifiOff, KeyRound } from 'lucide-react';
import { toast } from 'sonner';
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent,
  DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { ApiError } from '@/lib/api';
import { logout, useSession } from '@/lib/auth';
import { useInstances, type Instance } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useDebugActions } from '@/lib/use-debug-actions';
import { cn } from '@/lib/utils';
import { PasswordChangeDialog } from './PasswordChangeDialog';

const VERSION = import.meta.env.VITE_APP_VERSION ?? 'dev';

const HEALTH_BG: Record<NonNullable<Instance['health']> | 'unknown', string> = {
  available:   'bg-status-success',
  degraded:    'bg-status-warning',
  unavailable: 'bg-status-danger',
  unknown:     'bg-status-neutral',
};

export function TopBar({ onMenuClick }: { onMenuClick: () => void }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { data, isError } = useInstances();
  const { filter, setFilter } = useInstanceFilter();
  const dbg = useDebugActions();
  const instances = data?.instances ?? [];
  const { data: session } = useSession();
  const [pwOpen, setPwOpen] = useState(false);

  const onLogout = async () => {
    try { await logout(); toast.success('Signed out'); }
    catch (err) {
      if (!(err instanceof ApiError) || err.status !== 401) {
        toast.error('Logout failed', { description: err instanceof Error ? err.message : 'unknown error' });
      }
    } finally { qc.clear(); navigate('/login', { replace: true }); }
  };

  return (
    <header className="h-14 flex items-center gap-3 md:gap-6 px-3 md:px-5 border-b border-border bg-bg">
      <button type="button" onClick={onMenuClick} aria-label="Toggle navigation"
        className="md:hidden grid place-items-center w-8 h-8 border border-border rounded-md text-foreground-2 hover:bg-surface">
        <Menu className="w-4 h-4" />
      </button>

      <div className="flex items-center gap-2.5 font-semibold tracking-tight">
        <span className="w-[22px] h-[22px] grid place-items-center bg-accent text-accent-text rounded-[5px] font-mono font-bold text-[13px]">sf</span>
        <span>seasonfill</span>
        <span className="mono text-[11px] text-faint ml-1.5">v{VERSION}</span>
      </div>

      <div className="flex items-center gap-1.5 md:ml-4" role="group" aria-label="Instance filter">
        {instances.map((inst) => {
          const name = inst.name ?? '';
          if (!name) return null;
          const active = filter === name;
          const bg = HEALTH_BG[inst.health ?? 'unknown'];
          return (
            <Tooltip key={name}>
              <TooltipTrigger asChild>
                <button type="button" onClick={() => setFilter(active ? null : name)}
                  aria-pressed={active}
                  className={cn(
                    'inline-flex items-center gap-1.5 h-7 px-2.5 rounded-full border text-[12px] font-mono',
                    'border-border-faint bg-surface text-foreground-2 hover:bg-surface-2 hover:text-foreground',
                    active && 'bg-surface-2 text-foreground border-border-strong',
                  )}>
                  <span className={cn('inline-block w-1.5 h-1.5 rounded-full', bg)} />
                  {name}
                </button>
              </TooltipTrigger>
              <TooltipContent side="bottom">Filter by {name}</TooltipContent>
            </Tooltip>
          );
        })}
      </div>

      <div className="flex-1" />

      <div className="hidden md:flex items-center gap-2 font-mono text-[12px] text-faint">
        <span className={cn('inline-block w-1.5 h-1.5 rounded-full', isError ? 'bg-status-danger' : 'bg-status-success')} />
        {isError ? 'connection lost' : 'all systems nominal'}
      </div>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button type="button" aria-label="Account menu"
            className="grid place-items-center w-8 h-8 border border-border rounded-md text-foreground-2 hover:bg-surface">
            <Settings className="w-4 h-4" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-[220px]">
          <DropdownMenuLabel>
            {session?.username ? session.username : 'Account'}
          </DropdownMenuLabel>
          <DropdownMenuItem onSelect={() => setPwOpen(true)}>
            <KeyRound className="w-3.5 h-3.5 mr-2" /> Change password
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onLogout}>
            <LogOut className="w-3.5 h-3.5 mr-2" /> Logout
            <span className="ml-auto mono text-[11px] text-faint">⌘⇧Q</span>
          </DropdownMenuItem>
          {import.meta.env.DEV && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>Debug</DropdownMenuLabel>
              <DropdownMenuItem onSelect={() => dbg.simulate401()}>
                <ShieldAlert className="w-3.5 h-3.5 mr-2" /> Simulate 401
                <span className="ml-auto mono text-[11px] text-faint">expire token</span>
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => dbg.simulateNetLoss()}>
                <WifiOff className="w-3.5 h-3.5 mr-2" /> Simulate net loss
                <span className="ml-auto mono text-[11px] text-faint">8s outage</span>
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <PasswordChangeDialog open={pwOpen} onOpenChange={setPwOpen} />
    </header>
  );
}
