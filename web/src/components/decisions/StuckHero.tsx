import { useState, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertOctagon, ArrowUpRight, X } from 'lucide-react';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import type { StuckSeason } from '@/lib/api/decisions';

const DISMISS_KEY = 'seasonfill:decisions:stuckDismissed';

export interface StuckHeroProps {
  readonly items: readonly StuckSeason[] | undefined;
  readonly isLoading: boolean;
  readonly onOpenSeason: (s: StuckSeason) => void;
}

function readDismissed(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.sessionStorage.getItem(DISMISS_KEY) === 'true';
  } catch {
    return false;
  }
}

function writeDismissed() {
  if (typeof window === 'undefined') return;
  try {
    window.sessionStorage.setItem(DISMISS_KEY, 'true');
  } catch {
    // ignore — private browsing or storage quota
  }
}

export function StuckHero({ items, isLoading, onOpenSeason }: StuckHeroProps) {
  const { t } = useTranslation();
  const [dismissed, setDismissed] = useState<boolean>(() => readDismissed());

  useEffect(() => {
    if (dismissed) writeDismissed();
  }, [dismissed]);

  const onDismiss = useCallback(() => setDismissed(true), []);

  if (dismissed) return null;
  if (isLoading) {
    return (
      <div className="mb-5 rounded-lg border border-status-warning/30 bg-surface overflow-hidden">
        <div className="px-4 py-3 border-b border-border-faint bg-status-warning/8 flex items-center gap-2.5">
          <Skeleton className="h-4 w-4" />
          <Skeleton className="h-4 w-32" />
        </div>
        <div className="p-4 space-y-2">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-2/3" />
        </div>
      </div>
    );
  }
  if (!items || items.length === 0) return null;

  return (
    <section
      data-testid="stuck-hero"
      className="mb-5 rounded-lg border border-status-warning/30 bg-surface overflow-hidden"
      aria-labelledby="stuck-hero-title"
    >
      <header className="flex items-center gap-2.5 px-4 py-3 border-b border-border-faint bg-status-warning/8">
        <AlertOctagon className="size-4 text-status-warning" aria-hidden="true" />
        <h3 id="stuck-hero-title" className="text-[13.5px] font-semibold">
          {t('decisions.stuck.title')}
        </h3>
        <span className="text-[12px] text-tx-muted">
          {t('decisions.stuck.subtitle')}
        </span>
        <span className="flex-1" />
        <button
          type="button"
          onClick={onDismiss}
          className="text-tx-faint hover:text-tx-primary transition-colors"
          aria-label={t('decisions.stuck.dismiss')}
        >
          <X className="size-3.5" />
        </button>
      </header>
      <ul>
        {items.map((it) => (
          <li
            key={`${it.instance}:${it.seriesId}:${it.seasonNumber}`}
            className={cn(
              'grid grid-cols-[1fr_120px_150px_auto] gap-3 items-center px-4 py-2.5',
              'border-b border-border-faint last:border-b-0 cursor-pointer',
              'hover:bg-surface-2 group',
            )}
            onClick={() => onOpenSeason(it)}
            role="button"
            tabIndex={0}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onOpenSeason(it);
              }
            }}
            aria-label={t('decisions.stuck.rowAria', {
              series: it.seriesTitle,
              season: `S${String(it.seasonNumber).padStart(2, '0')}`,
              count: it.consecutive,
            })}
          >
            <span className="font-semibold text-[13.5px]">
              {it.seriesTitle}
              <span className="ml-1.5 font-mono text-tx-muted font-normal">
                S{String(it.seasonNumber).padStart(2, '0')}
              </span>
            </span>
            <span className="font-mono text-[12px] text-status-warning">
              {t('decisions.stuck.consecutive', { count: it.consecutive })}
            </span>
            <span className="font-mono text-[11px] text-tx-faint">{it.lastReason}</span>
            <span className="text-tx-faint group-hover:text-accent flex justify-end">
              <ArrowUpRight className="size-3.5" />
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

// Test-only escape hatch — call to clear the dismissed flag in test
// teardown so successive renders don't leak state.
// eslint-disable-next-line react-refresh/only-export-components
export function _clearStuckDismissedForTests() {
  if (typeof window === 'undefined') return;
  try { window.sessionStorage.removeItem(DISMISS_KEY); } catch { /* ignore */ }
}
