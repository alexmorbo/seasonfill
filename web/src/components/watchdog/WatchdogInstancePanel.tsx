import { useTranslation } from 'react-i18next';
import { Power, Settings as SettingsIcon } from 'lucide-react';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { Sparkline } from '@/components/ui/sparkline';
import { cn } from '@/lib/utils';
import { useQbitSettings } from '@/api/qbit';
import { useWatchdogToggle } from '@/lib/api/watchdogToggle';
import type { WatchdogRollup } from '@/lib/api/watchdogRollups';

export interface WatchdogInstancePanelProps {
  rollup: WatchdogRollup;
  sparkline?: number[];
  onOpenInstanceForm?: (instance: string) => void;
}

export function WatchdogInstancePanel({
  rollup,
  sparkline = [],
  onOpenInstanceForm,
}: WatchdogInstancePanelProps) {
  const { t } = useTranslation();
  const toggle = useWatchdogToggle();
  const settings = useQbitSettings(rollup.instance_name);
  const toggleReady = Boolean(settings.data);

  const handleToggle = (next: boolean) => {
    if (!settings.data) return;
    toggle.mutate({
      instance: rollup.instance_name,
      enabled: next,
      current: settings.data,
    });
  };
  const handleConfigure = () => onOpenInstanceForm?.(rollup.instance_name);
  const isOff = !rollup.enabled;
  const pollMinutes = Math.max(1, Math.round(rollup.poll_interval_seconds / 60));

  return (
    <Card
      data-testid={`watchdog-panel-${rollup.instance_name}`}
      className={cn('flex flex-col gap-3 p-3.5', isOff && 'opacity-90')}
    >
      <div className="flex items-center gap-2.5">
        <h3 className="flex-1 inline-flex items-center gap-2 text-[15px] font-semibold min-w-0">
          <span className="truncate">{rollup.instance_name}</span>
          {!isOff && (
            <span
              data-testid={`watchdog-panel-reachable-${rollup.instance_name}`}
              className={cn(
                'inline-block h-2 w-2 rounded-full shrink-0',
                rollup.qbit_reachable ? 'bg-ok' : 'bg-warn',
              )}
              aria-label={
                rollup.qbit_reachable
                  ? t('watchdog.config.qbit.reachable')
                  : t('watchdog.config.qbit.unreachable')
              }
              title={
                rollup.qbit_reachable
                  ? t('watchdog.config.qbit.reachable')
                  : t('watchdog.config.qbit.unreachable')
              }
            />
          )}
        </h3>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-tx-muted hover:text-foreground"
          onClick={handleConfigure}
          aria-label={t('watchdog.config.cta.configure')}
          data-testid={`watchdog-panel-configure-${rollup.instance_name}`}
        >
          <SettingsIcon className="h-4 w-4" />
        </Button>
        <Switch
          data-testid={`watchdog-panel-toggle-${rollup.instance_name}`}
          checked={rollup.enabled}
          disabled={toggle.isPending || !toggleReady}
          onCheckedChange={handleToggle}
          aria-label={
            rollup.enabled
              ? t('watchdog.config.toggle.on')
              : t('watchdog.config.toggle.off')
          }
        />
      </div>

      {isOff ? (
        <>
          <span className="text-[12.5px] text-tx-muted">
            {t('watchdog.config.disabled')}
          </span>
          <Button
            variant="outline" size="sm" className="justify-center"
            onClick={handleConfigure}
            data-testid={`watchdog-panel-enable-${rollup.instance_name}`}
          >
            <Power className="mr-1 h-4 w-4" />
            {t('watchdog.config.cta.enable')}
          </Button>
        </>
      ) : (
        <>
          <div className="flex flex-wrap gap-1.5">
            <Badge variant="solid" mono>
              {t('watchdog.config.chips.poll', { n: pollMinutes })}
            </Badge>
            <Badge variant="solid" mono>
              {t('watchdog.config.chips.cooldown', { n: rollup.cooldown_hours })}
            </Badge>
            <Badge variant="solid" mono>
              {t('watchdog.config.chips.noBetterMax', { n: rollup.no_better_max })}
            </Badge>
          </div>
          <div className="flex items-end gap-2">
            <Sparkline
              data={sparkline}
              ariaLabel={`regrab-sparkline-${rollup.instance_name}`}
              className="h-[30px] flex-1"
            />
            <span className="text-[10px] uppercase tracking-wide text-tx-faint">
              {t('watchdog.config.sparkline.label', { n: rollup.regrabs_7d })}
            </span>
          </div>
        </>
      )}
    </Card>
  );
}
