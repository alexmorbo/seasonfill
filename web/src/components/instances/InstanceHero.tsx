import { useTranslation } from 'react-i18next';
import { Pencil, Play, RefreshCw, Trash2, ExternalLink, Loader2 } from 'lucide-react';
import type { Instance } from '@/lib/instances';
import { useCountersAggregate } from '@/lib/api/counters';
import { useMissing } from '@/lib/missing';
import { useWebhookStatus } from '@/lib/webhook-status';
import { useQbitSettings } from '@/lib/qbit-settings';
import { useForceScanButton } from '@/lib/scan-mutations';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Sparkline } from '@/components/ui/sparkline';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { pickPublicHref } from '@/lib/url';
import { KIND_CLASS, KIND_DOT, healthKind, healthLabelKey } from '@/lib/badge-variants';
import { InstanceStatsBlock } from './InstanceStatsBlock';
import { InstanceChipRow } from './InstanceChipRow';

export interface InstanceHeroProps {
  readonly instance: Instance;
  readonly onEdit: (name: string) => void;
  readonly onRecheck: (name: string) => void;
  readonly onDelete: (name: string) => void;
}

/**
 * Rich instance card, rendered for every instance in the list. Reads
 * counters (24h + 7d), missing count, webhook status, qBit settings.
 * Renders sparkline from the 7d `counters.sparkline` slice. Degradation
 * tint applies when health != 'Available' (except Bootstrapping, which
 * stays neutral). Actions: Edit · Force-scan · Recheck · Open Sonarr · Delete.
 */
export function InstanceHero({ instance, onEdit, onRecheck, onDelete }: InstanceHeroProps) {
  const { t } = useTranslation();
  const name = instance.name ?? '';
  const c24 = useCountersAggregate('24h');
  const c7 = useCountersAggregate('7d');
  const row24 = c24.data?.items.find((i) => i.instance_name === name);
  const row7 = c7.data?.items.find((i) => i.instance_name === name);
  const missing = useMissing(name);
  const webhook = useWebhookStatus(name);
  const qbit = useQbitSettings(name);
  const forceScan = useForceScanButton(name);

  // Ф6-R-6b: radarr instances now appear in this list. The hero's stats,
  // sparkline and chip row are Sonarr-shaped (episodes missing / webhook /
  // qBit watchdog) and would render broken/empty for a radarr row, so they are
  // hidden. The connection hooks above still fire (rules-of-hooks); their
  // (possibly 404) results are simply not surfaced for radarr.
  const isRadarr = (instance.type ?? 'sonarr') === 'radarr';
  const kind = healthKind(instance.health);
  // Story 488 (B-14): Bootstrapping renders neutral pill + spinner,
  // and must NOT trigger the red degraded border treatment — the
  // operator hasn't seen a real failure yet.
  const bootstrapping = instance.health === 'Bootstrapping';
  const degraded = kind !== 'success' && !bootstrapping;
  // Self-throttled wears a warning (amber) accent rather than the red
  // danger accent — same border treatment, different colour token.
  const warn = kind === 'warning';
  const flips = instance.transitions_count ?? 0;
  const sparkData = (row7?.sparkline ?? []).map((b) => b.grabs);
  const sonarrHref = pickPublicHref(instance.public_url, instance.url);

  return (
    <Card
      data-testid={`instance-hero-${name}`}
      className={cn(
        'p-[18px] flex flex-col gap-4',
        degraded && (warn
          ? 'border-l-[3px] border-l-status-warning'
          : 'border-l-[3px] border-l-status-danger'),
      )}
    >
      <CardContent className="p-0 flex flex-col gap-4">
        <div className="flex items-start gap-4">
          <div className="flex-1 min-w-0 flex flex-col gap-1.5">
            <div className="flex items-center gap-2.5">
              <h3 className="text-[18px] font-[650] tracking-tight m-0">{name}</h3>
              <Badge variant="neutral" mono data-testid={`hero-type-${name}`}>
                {t(`instances.type.${isRadarr ? 'radarr' : 'sonarr'}`)}
              </Badge>
              <span
                className={cn(
                  'inline-flex items-center gap-1 px-1.5 h-[18px] rounded border font-mono text-[10.5px]',
                  KIND_CLASS[healthKind(instance.health)],
                )}
                data-testid={`hero-health-${name}`}
              >
                {instance.health === 'Bootstrapping' ? (
                  <Loader2
                    className="w-3 h-3 animate-spin text-tx-faint"
                    aria-hidden="true"
                    data-testid={`hero-health-spinner-${name}`}
                  />
                ) : (
                  <span className={cn('inline-block w-1.5 h-1.5 rounded-full', KIND_DOT[healthKind(instance.health)])} />
                )}
                {t(healthLabelKey(instance.health))} · {relativeTime(instance.last_check_at)}
              </span>
              <Badge variant="solid" mono>{instance.mode ?? 'auto'}</Badge>
              {flips > 0 && (
                <Badge variant="warn" mono data-testid={`hero-flips-${name}`}>
                  {t('instances.compact.degradation.flips', { count: flips })}
                </Badge>
              )}
            </div>
            <div className="font-mono text-[12.5px] text-tx-muted">
              {t('instances.hero.subline', {
                dryRun: t('instances.hero.dryRun.off'),
                url: instance.url ?? '',
                lastCheck: relativeTime(instance.last_check_at),
              })}
            </div>
            {degraded && instance.last_error && (
              <div
                data-testid="hero-error"
                className={cn(
                  'font-mono text-[12px] break-all',
                  warn ? 'text-status-warning' : 'text-status-danger',
                )}
              >
                {instance.last_error}
              </div>
            )}
          </div>
          <div className="flex gap-2 flex-none">
            <Button size="sm" variant="outline" onClick={() => onEdit(name)}>
              <Pencil className="w-3.5 h-3.5 mr-1.5" />
              {t('instances.hero.actions.edit')}
            </Button>
            {!isRadarr && (
              <Button
                size="sm"
                variant="primary"
                onClick={forceScan.start}
                disabled={forceScan.disabled}
                data-testid={`hero-force-scan-${name}`}
                data-busy={forceScan.disabled ? 'true' : 'false'}
              >
                {forceScan.disabled ? (
                  <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" aria-hidden="true" />
                ) : (
                  <Play className="w-3.5 h-3.5 mr-1.5" />
                )}
                {forceScan.disabled
                  ? t('instances.hero.actions.forceScanRunning')
                  : t('instances.hero.actions.forceScan')}
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              onClick={() => onRecheck(name)}
              data-testid={`hero-recheck-${name}`}
            >
              <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
              {t('instances.compact.actions.recheck')}
            </Button>
            {sonarrHref && (
              <Button size="sm" variant="outline" asChild>
                <a href={sonarrHref} target="_blank" rel="noreferrer"
                   data-testid={`hero-sonarr-link-${name}`}>
                  <ExternalLink className="w-3.5 h-3.5 mr-1.5" />
                  {t(isRadarr ? 'instances.hero.actions.openRadarr' : 'instances.hero.actions.openSonarr')}
                </a>
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              onClick={() => onDelete(name)}
              data-testid={`hero-delete-${name}`}
              className="text-status-danger"
            >
              <Trash2 className="w-3.5 h-3.5 mr-1.5" />
              {t('common.delete')}
            </Button>
          </div>
        </div>

        {!isRadarr && (
        <div className="flex items-end gap-[30px] flex-wrap py-[15px] border-y border-border-faint">
          {c24.isPending ? (
            <Skeleton className="h-12 w-32" />
          ) : (
            <InstanceStatsBlock
              grabs={row24?.totals.grabs ?? 0}
              imports={row24?.totals.imports ?? 0}
              fails={row24?.totals.fails ?? 0}
              windowLabelKey="instances.hero.stats.24h.label"
            />
          )}
          {c7.isPending ? (
            <Skeleton className="h-12 w-32" />
          ) : (
            <InstanceStatsBlock
              grabs={row7?.totals.grabs ?? 0}
              imports={row7?.totals.imports ?? 0}
              fails={row7?.totals.fails ?? 0}
              windowLabelKey="instances.hero.stats.7d.label"
              separator
            />
          )}
          <div className="ml-auto flex flex-col gap-1.5 items-start min-w-[170px]" data-testid="hero-sparkline">
            <Sparkline
              data={sparkData}
              ariaLabel={t('instances.hero.sparkline.aria', { instance: name })}
              className="h-[42px] w-full"
            />
            <span className="text-[10px] font-semibold tracking-[0.08em] uppercase text-tx-faint">
              {t('instances.hero.sparkline.label')}
            </span>
          </div>
        </div>
        )}

        {!isRadarr && (
          <InstanceChipRow
            instanceName={name}
            missingCount={missing.data?.items?.length}
            qbitSettings={qbit.data}
            webhookStatus={webhook.data}
          />
        )}
      </CardContent>
    </Card>
  );
}
