import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, AlertTriangle, Download, RotateCw, ChevronDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import { buildChips, type Grab } from '@/lib/grabs/chipBuilder';
import { formatEpisodeRange, formatImportDuration } from '@/lib/grabs/format';
import { relativeTime } from '@/lib/format';
import { ChipsRow } from '@/components/grabs/ChipsRow';
import { ReGrabThread } from '@/components/grabs/ReGrabThread';

export interface GrabRowProps {
  grab: Grab;
  selected: boolean;
  threadOpen: boolean;
  reGrabIndex: number | null;     // 1, 2, 3 … or null (computed by parent from replayed_by chain)
  instance?: string | null;
  localAll?: readonly Grab[];     // for ReGrabThread ancestor walk
  onOpenDrawer: (id: string) => void;
  onToggleThread: (id: string) => void;
}

const STATUS_META: Record<string, { variant: 'imported' | 'grabbed' | 'failed' | 'muted'; icon: typeof Check }> = {
  imported:      { variant: 'imported', icon: Check },
  grabbed:       { variant: 'grabbed',  icon: Download },
  import_failed: { variant: 'failed',   icon: AlertTriangle },
  grab_failed:   { variant: 'failed',   icon: AlertTriangle },
  expired:       { variant: 'muted',    icon: AlertTriangle },
};

const STATUS_CLASS: Record<string, string> = {
  imported: 'text-ok bg-ok-dim',
  grabbed:  'text-info bg-info/14',
  failed:   'text-danger bg-danger-dim',
  muted:    'text-tx-muted bg-bg-surface-2',
};

export function GrabRow({
  grab, selected, threadOpen, reGrabIndex, instance, localAll,
  onOpenDrawer, onToggleThread,
}: GrabRowProps) {
  const { t } = useTranslation();
  const status = grab.status ?? 'grabbed';
  const meta = STATUS_META[status] || STATUS_META['grabbed']!;
  const StatusIcon = meta.icon;
  const isFailRow = status === 'import_failed' || status === 'grab_failed';

  const epRange = useMemo(
    () => formatEpisodeRange(grab.season_number ?? 0, undefined, grab.coverage_count ?? undefined),
    [grab.season_number, grab.coverage_count],
  );
  const chips = useMemo(() => buildChips({ grab, episodeRangeLabel: epRange }), [grab, epRange]);

  // Poster gradient hue derived from series_id (stable per series). Range 0–360.
  const ph = ((grab.series_id ?? 0) * 37) % 360;

  const handleRowClick = () => onOpenDrawer(grab.id ?? '');
  const handleRowKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleRowClick(); }
  };
  const handleThreadClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    onToggleThread(grab.id ?? '');
  };

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={handleRowClick}
      onKeyDown={handleRowKey}
      aria-label={t('grabs.row.openAria', { id: grab.id ?? '' })}
      data-testid={`grab-row-${grab.id ?? 'unknown'}`}
      data-failrow={isFailRow ? 'true' : 'false'}
      data-selected={selected ? 'true' : 'false'}
      className={cn(
        'flex gap-3 p-3 rounded-md cursor-pointer items-start transition-colors',
        'border bg-bg-surface border-border-faint hover:bg-bg-surface-2 hover:border-border-strong',
        selected && 'border-accent shadow-[inset_0_0_0_1px_var(--color-accent)]',
        isFailRow && [
          'border-[oklch(0.70_0.17_25_/_0.4)]',
          'bg-[linear-gradient(90deg,var(--color-danger-dim),transparent_36%),var(--color-bg-surface)]',
        ],
      )}
    >
      {/* poster thumb */}
      <div
        aria-hidden="true"
        className="w-[38px] h-[57px] rounded-[5px] flex-none border border-border-subtle"
        style={{
          background: `radial-gradient(120% 80% at 30% 0%, oklch(0.34 0.07 ${ph}), oklch(0.19 0.04 ${(ph + 30) % 360}))`,
        }}
      />
      {/* main */}
      <div className="flex-1 min-w-0 flex flex-col gap-1.5">
        <div className="flex items-center gap-2.5">
          {reGrabIndex !== null && reGrabIndex > 0 && (
            <button
              type="button"
              onClick={handleThreadClick}
              data-testid={`regrab-tag-${grab.id ?? 'unknown'}`}
              aria-expanded={threadOpen}
              aria-label={t('grabs.regrab.thread.toggle')}
              className={cn(
                'inline-flex items-center gap-1 rounded-[5px] border px-1.5 py-px',
                'font-mono text-[10.5px] font-semibold cursor-pointer',
                'text-warn bg-warn-dim border-warn/30 hover:bg-warn/20',
              )}
            >
              <RotateCw className="size-3" />
              {t('grabs.regrab.tag', { count: reGrabIndex })}
              <ChevronDown
                className={cn('size-3 transition-transform', threadOpen && 'rotate-180')}
              />
            </button>
          )}
          <span className="text-[14px] font-semibold tracking-tight truncate">
            {grab.series_title ?? '—'}
          </span>
          <div className="flex-1" />
          <span
            className={cn(
              'text-[11px] font-semibold px-2.5 py-px rounded-full',
              'inline-flex items-center gap-1.5 whitespace-nowrap',
              STATUS_CLASS[meta.variant],
            )}
          >
            <StatusIcon className="size-3" />
            {t(`grabs.status.${status}`, { defaultValue: status })}
          </span>
        </div>
        <ChipsRow chips={chips} />
        <div className="flex items-center gap-2 text-[11.5px] text-tx-faint font-mono">
          {grab.indexer_name && <span>{grab.indexer_name}</span>}
          {isFailRow && (
            <>
              <DotSep />
              <span>{t('grabs.row.attempts', { count: grab.attempts ?? 0 })}</span>
              {grab.error_message && (
                <>
                  <DotSep />
                  <span
                    className="text-danger truncate min-w-0 max-w-[420px]"
                    title={grab.error_message}
                  >
                    «{grab.error_message}»
                  </span>
                </>
              )}
            </>
          )}
          {!isFailRow && (
            <>
              <DotSep />
              <span>{relativeTime(grab.updated_at ?? grab.created_at)}</span>
              {grab.status === 'imported' && (
                <>
                  <DotSep />
                  <span>
                    {t('grabs.row.import', {
                      duration: formatImportDuration(grab.created_at, grab.updated_at),
                    })}
                  </span>
                </>
              )}
            </>
          )}
        </div>
        {threadOpen && instance && (
          <ReGrabThread
            instance={instance}
            grab={grab}
            all={localAll ?? []}
            open={threadOpen}
          />
        )}
      </div>
    </div>
  );
}

function DotSep() {
  return <span aria-hidden="true" className="size-[2px] rounded-full bg-tx-faint opacity-60" />;
}
