import { useState } from 'react';
import { useTranslation } from 'react-i18next';
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
import { useAuthConfig } from '@/lib/auth-config';
import { useInstances } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useDebugActions } from '@/lib/use-debug-actions';
import { KIND_DOT, healthKind } from '@/lib/badge-variants';
import { cn } from '@/lib/utils';
import { PasswordChangeDialog } from './PasswordChangeDialog';
import { LanguageSwitcher } from './LanguageSwitcher';

const VERSION = import.meta.env.VITE_APP_VERSION ?? 'dev';

export function TopBar({ onMenuClick }: { onMenuClick: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { data, isError } = useInstances();
  const { filter, setFilter } = useInstanceFilter();
  const dbg = useDebugActions();
  const instances = data?.instances ?? [];
  const { data: session } = useSession();
  const cfg = useAuthConfig();
  const [pwOpen, setPwOpen] = useState(false);

  // Formal auth surface (Logout + Change password) only makes sense when
  // we own the credential flow. Under Basic, the browser controls the
  // popup; under None, there's nothing to sign out from. Default to
  // 'forms' when the config is still loading to avoid hiding controls
  // mid-flight on first paint.
  const mode = cfg.data?.mode ?? 'forms';
  const showAccountControls = mode === 'forms' || mode === 'oidc';
  const showPasswordChange = mode === 'forms';
  const accountLabel = showAccountControls
    ? (session?.username ?? t('nav.account'))
    : t('nav.anonymous');

  const onLogout = async () => {
    try { await logout(); toast.success(t('nav.signedOut')); }
    catch (err) {
      if (!(err instanceof ApiError) || err.status !== 401) {
        toast.error(t('nav.logoutFailed'), { description: err instanceof Error ? err.message : t('common.unknown') });
      }
    } finally { qc.clear(); navigate('/login', { replace: true }); }
  };

  return (
    <header className="h-14 flex items-center gap-3 md:gap-6 px-3 md:px-5 border-b border-border bg-bg">
      <button type="button" onClick={onMenuClick} aria-label={t('nav.toggleNav')}
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
          const bg = KIND_DOT[healthKind(inst.health)];
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
        {isError ? t('nav.connectionLost') : t('nav.allSystemsNominal')}
      </div>

      <LanguageSwitcher />

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button type="button" aria-label={t('nav.accountMenu')}
            className="grid place-items-center w-8 h-8 border border-border rounded-md text-foreground-2 hover:bg-surface">
            <Settings className="w-4 h-4" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-[220px]">
          <DropdownMenuLabel>
            {accountLabel}
          </DropdownMenuLabel>
          {showPasswordChange && (
            <DropdownMenuItem onSelect={() => setPwOpen(true)}>
              <KeyRound className="w-3.5 h-3.5 mr-2" /> {t('nav.changePassword')}
            </DropdownMenuItem>
          )}
          {showAccountControls && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={onLogout}>
                <LogOut className="w-3.5 h-3.5 mr-2" /> {t('nav.logout')}
                <span className="ml-auto mono text-[11px] text-faint">⌘⇧Q</span>
              </DropdownMenuItem>
            </>
          )}
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
