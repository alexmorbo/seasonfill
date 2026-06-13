// Settings → General → Timezone section. Wired below the "Scan tuning"
// block inside GeneralTab (one tab, three blocks). The control is a
// single Radix Select over IANA zones with a current-source pill and a
// restart-required banner that surfaces after the first PATCH.

import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Clock, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
} from '@/components/ui/select';
import {
  useTimezoneState, listIANAZones,
} from '@/lib/timezone';
import { useUpdateTimezone } from '@/lib/useTimezoneSetting';

export function TimezoneSection() {
  const { t } = useTranslation();
  const { state, isLoading } = useTimezoneState();
  const mut = useUpdateTimezone();

  const zones = useMemo(() => listIANAZones(), []);
  // Track which server value we last "received" so that when the server
  // state changes (initial query resolves, or another tab PATCHes), we
  // re-anchor the local Select WITHOUT a setState-in-effect cascade.
  const [lastServer, setLastServer] = useState<string>(state.timezone);
  const [selected, setSelected] = useState<string>(state.timezone);
  if (state.timezone !== lastServer) {
    setLastServer(state.timezone);
    setSelected(state.timezone);
  }
  const [dismissedRestart, setDismissedRestart] = useState(false);

  const dirty = selected !== state.timezone;

  const onSave = () => {
    if (!selected || selected === state.timezone) return;
    mut.mutate(selected, {
      onSuccess: () => setDismissedRestart(false),
    });
  };

  const sourceLabel = t(`settings.timezone.source.${state.source}`);
  const showRestartBanner = state.requiresRestart && !dismissedRestart;

  return (
    <section
      data-testid="timezone-section"
      className="flex flex-col gap-3.5"
    >
      <header className="flex flex-col gap-[3px]">
        <h2 className="text-[15px] font-[650] tracking-[-0.01em] m-0 flex items-center gap-2">
          <Clock className="w-4 h-4 text-tx-muted" aria-hidden="true" />
          {t('settings.timezone.title')}
        </h2>
        <p className="text-[12.5px] text-muted m-0">
          {t('settings.timezone.description')}
        </p>
      </header>

      {showRestartBanner && (
        <Alert
          variant="default"
          data-testid="timezone-restart-banner"
          className="border-status-warn/50 bg-status-warn/10"
        >
          <RefreshCw className="w-4 h-4" />
          <AlertTitle>{t('settings.timezone.restart.title')}</AlertTitle>
          <AlertDescription className="flex items-start justify-between gap-3">
            <span>{t('settings.timezone.restart.body')}</span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setDismissedRestart(true)}
            >
              {t('common.dismiss')}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      <div className="grid grid-cols-[1fr_auto] gap-3.5 items-end">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="timezone-select">
            {t('settings.timezone.fieldLabel')}
          </Label>
          <Select
            value={selected}
            onValueChange={(v) => {
              // Radix may emit '' on a programmatic open/close — guard.
              if (v) setSelected(v);
            }}
            disabled={isLoading || mut.isPending}
          >
            <SelectTrigger
              id="timezone-select"
              data-testid="timezone-select-trigger"
              className="font-mono"
            >
              <SelectValue placeholder={t('common.loading')} />
            </SelectTrigger>
            <SelectContent className="max-h-[320px]">
              {zones.map((z) => (
                <SelectItem key={z} value={z} className="font-mono">
                  {z}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex items-center gap-1.5 text-[11.5px] text-tx-muted">
            <span data-testid="timezone-source-pill">
              {t('settings.timezone.sourceLabel')}: <b>{sourceLabel}</b>
            </span>
          </div>
        </div>
        <Button
          type="button"
          onClick={onSave}
          disabled={!dirty || mut.isPending}
          data-testid="timezone-save-button"
        >
          {mut.isPending ? t('common.saving') : t('settings.timezone.save')}
        </Button>
      </div>
    </section>
  );
}
