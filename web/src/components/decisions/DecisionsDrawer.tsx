import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Sheet, SheetContent } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { EmptyState } from '@/components/EmptyState';
import {
  useDecisionsSeason,
} from '@/lib/api/decisions-season';
import type { DecisionsWindow } from '@/lib/api/decisions';
import { DecisionsTimeline } from './DecisionsTimeline';

export interface DecisionsDrawerProps {
  readonly open: boolean;
  readonly seriesId: number | null;
  readonly seriesTitle: string | null;
  readonly seasonNumber: number | null;
  readonly instance: string | null;
  readonly window: DecisionsWindow;
  readonly onOpenChange: (next: boolean) => void;
}

const WINDOW_LABEL_KEY: Record<DecisionsWindow, string> = {
  '24h': 'decisions.window.h24',
  '7d':  'decisions.window.d7',
  '30d': 'decisions.window.d30',
  'all': 'decisions.window.all',
};

export function DecisionsDrawer(props: DecisionsDrawerProps) {
  const { t, i18n } = useTranslation();
  const { open, seriesId, seriesTitle, seasonNumber, instance, window, onOpenChange } = props;
  const q = useDecisionsSeason({
    seriesId, seasonNumber, window, enabled: open, lang: i18n.resolvedLanguage ?? '',
  });

  const seasonLabel = seasonNumber !== null ? `S${String(seasonNumber).padStart(2, '0')}` : '';
  const title = `${seriesTitle ?? '—'} · ${seasonLabel}`.trim();
  const metaInstance = instance ?? '—';

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full sm:max-w-md overflow-y-auto p-0 flex flex-col"
        data-testid="decisions-drawer"
      >
        <header className="px-5 pt-5 pb-3 border-b border-border-faint">
          <h3 className="text-[15px] font-semibold tracking-tight">
            {title}
          </h3>
          <div className="text-[12px] text-tx-faint font-mono mt-0.5">
            {metaInstance} · {t('decisions.drawer.history')}
          </div>
        </header>
        <div className="px-5 py-4 flex flex-col gap-4 flex-1">
          {q.isPending && (
            <div className="space-y-3" data-testid="drawer-skeleton">
              <Skeleton className="h-4 w-1/3" />
              <Skeleton className="h-3 w-2/3" />
              <Skeleton className="h-3 w-1/2" />
              <Skeleton className="h-3 w-2/3" />
            </div>
          )}
          {q.isError && (
            <EmptyState
              title={t('decisions.drawer.loadFailedTitle')}
              body={q.error.message}
            />
          )}
          {q.data && (
            <>
              <div className="flex gap-1.5 flex-wrap">
                <Chip>
                  {t('decisions.drawer.summary.decisions', {
                    count: q.data.rows.length,
                    window: t(WINDOW_LABEL_KEY[window]),
                  })}
                </Chip>
                <Chip variant="accent">
                  {t('decisions.drawer.summary.grabs', { count: q.data.grabCount })}
                </Chip>
                <Chip>
                  {t('decisions.drawer.summary.cooldown', { count: q.data.cooldownCount })}
                </Chip>
              </div>
              <DecisionsTimeline rows={q.data.rows} />
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Chip({ children, variant = 'neutral' }:
  { children: ReactNode; variant?: 'neutral' | 'accent' }) {
  return (
    <span
      className={
        variant === 'accent'
          ? 'inline-flex items-center px-2 h-[22px] rounded-full border border-accent text-accent font-mono text-[11px]'
          : 'inline-flex items-center px-2 h-[22px] rounded-full border border-border-faint text-tx-muted font-mono text-[11px]'
      }
    >
      {children}
    </span>
  );
}
