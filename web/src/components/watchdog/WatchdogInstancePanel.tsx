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
  const settings = useQbitSettings(rollup.instance);
  const toggleReady = Boolean(settings.data);

  const handleToggle = (next: boolean) => {
    if (!settings.data) return;
    toggle.mutate({
      instance: rollup.instance,
      enabled: next,
      current: settings.data,
    });
  };
  const handleConfigure = () => onOpenInstanceForm?.(rollup.instance);
  const isOff = !rollup.enabled;

  return (
    <Card
      data-testid={`watchdog-panel-${rollup.instance}`}
      className={cn('flex flex-col gap-3 p-3.5', isOff && 'opacity-90')}
    >
      <div className="flex items-center gap-2.5">
        <h3 className="flex-1 text-[15px] font-semibold">{rollup.instance}</h3>
        <Switch
          data-testid={`watchdog-panel-toggle-${rollup.instance}`}
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
            data-testid={`watchdog-panel-enable-${rollup.instance}`}
          >
            <Power className="mr-1 h-4 w-4" />
            {t('watchdog.config.cta.enable')}
          </Button>
        </>
      ) : (
        <>
          <Badge
            variant={rollup.qbit_reachable ? 'ok' : 'warn'}
            className="self-start"
          >
            <span className={cn('chip-dot',
              rollup.qbit_reachable ? 'bg-ok' : 'bg-warn')} />
            {rollup.qbit_reachable
              ? t('watchdog.config.qbit.reachable')
              : t('watchdog.config.qbit.unreachable')}
          </Badge>
          <div className="flex flex-wrap gap-1.5">
            <Badge variant="solid" mono>
              {t('watchdog.config.chips.poll', { n: rollup.poll_interval_min })}
            </Badge>
            <Badge variant="solid" mono>
              {t('watchdog.config.chips.cooldown', { n: rollup.regrab_cooldown_h })}
            </Badge>
            <Badge variant="solid" mono>
              {t('watchdog.config.chips.noBetterMax', { n: rollup.max_no_better })}
            </Badge>
            <Badge variant="solid" mono>
              {t('watchdog.config.chips.watched', { n: rollup.watched })}
            </Badge>
          </div>
          <div className="flex items-end gap-2">
            <Sparkline
              data={sparkline}
              ariaLabel={`regrab-sparkline-${rollup.instance}`}
              className="h-[30px] flex-1"
            />
            <span className="text-[10px] uppercase tracking-wide text-tx-faint">
              {t('watchdog.config.sparkline.label', { n: rollup.regrabs_7d })}
            </span>
          </div>
          <Button
            variant="outline" size="sm" className="justify-center"
            onClick={handleConfigure}
            data-testid={`watchdog-panel-configure-${rollup.instance}`}
          >
            <SettingsIcon className="mr-1 h-4 w-4" />
            {t('watchdog.config.cta.configure')}
          </Button>
        </>
      )}
    </Card>
  );
}
