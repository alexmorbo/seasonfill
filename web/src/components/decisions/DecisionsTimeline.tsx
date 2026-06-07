import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ArrowUpRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import type { Decision } from '@/lib/api/decisions';

export interface DecisionsTimelineProps {
  readonly rows: readonly Decision[];
}

type NodeVariant = 'grab' | 'block' | 'default';

function variantOf(d: Decision): NodeVariant {
  if (d.decision === 'grab') return 'grab';
  if (d.decision === 'blocked_cooldown') return 'block';
  return 'default';
}

function decisionChipKey(d: Decision): string {
  if (d.decision === 'grab') return 'decisions.timeline.chip.grab';
  if (d.decision === 'blocked_cooldown') return 'decisions.timeline.chip.block';
  return 'decisions.timeline.chip.skip';
}

export function DecisionsTimeline({ rows }: DecisionsTimelineProps) {
  const { t } = useTranslation();
  if (rows.length === 0) {
    return (
      <p className="text-[12.5px] text-tx-muted" data-testid="timeline-empty">
        {t('decisions.timeline.empty')}
      </p>
    );
  }
  return (
    <ol className="relative pl-5 before:content-[''] before:absolute before:left-[5px] before:top-1.5 before:bottom-1.5 before:w-[1.5px] before:bg-border-subtle"
        data-testid="decisions-timeline">
      {rows.map((d) => {
        const v = variantOf(d);
        return (
          <li
            key={d.id}
            className="relative pb-4 last:pb-0"
            data-variant={v}
          >
            <span
              className={cn(
                'absolute -left-[21px] top-1 size-2.5 rounded-full border-[1.5px]',
                v === 'grab' && 'bg-accent border-accent',
                v === 'block' && 'bg-status-warning border-status-warning',
                v === 'default' && 'bg-surface border-border-strong',
              )}
              aria-hidden="true"
            />
            <div className="flex items-center gap-2 mb-0.5">
              <span className="font-mono text-[11px] text-tx-faint">
                {relativeTime(d.created_at)}
              </span>
              {d.scan_run_id && (
                <Link
                  to={`/scans/${d.scan_run_id}${d.id ? `?drawer=${d.id}` : ''}`}
                  className="font-mono text-[11px] text-tx-muted hover:text-accent inline-flex items-center gap-0.5"
                >
                  scan {d.scan_run_id.slice(0, 4)}…{d.scan_run_id.slice(-4)}
                  <ArrowUpRight className="size-3" />
                </Link>
              )}
            </div>
            <div className="text-[12.5px] text-tx-secondary">
              <span
                className={cn(
                  'inline-flex items-center px-1.5 h-[18px] rounded-full border font-mono text-[10.5px] mr-2',
                  v === 'grab' && 'border-accent text-accent',
                  v === 'block' && 'border-status-warning text-status-warning',
                  v === 'default' && 'border-border-faint text-tx-muted',
                )}
              >
                {t(decisionChipKey(d))}
              </span>
              <span className="font-mono text-[11px] text-tx-faint mr-1">
                {d.reason ?? '—'}
              </span>
              {d.error_detail && (
                <span className="text-tx-muted">— {d.error_detail}</span>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
