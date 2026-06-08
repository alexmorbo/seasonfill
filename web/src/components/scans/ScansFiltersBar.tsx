import { useTranslation } from 'react-i18next';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Button } from '@/components/ui/button';

export type ScansFiltersValue = {
  status: string;   // 'all' | 'running' | 'completed' | 'failed' | 'aborted'
  trigger: string;  // 'all' | 'cron' | 'manual' | 'startup' | 'webhook'
  window: string;   // 'all' | '24h' | '7d' | '30d'
};

// reason: SCANS_DEFAULTS is the public API of this module, consumed by
// the Scans page. Splitting into a separate file is a 074 candidate,
// not in scope for the baseline cleanup.
// eslint-disable-next-line react-refresh/only-export-components
export const SCANS_DEFAULTS: ScansFiltersValue = {
  status: 'all', trigger: 'all', window: '7d',
};

const STATUSES = ['running', 'completed', 'failed', 'aborted'] as const;
const TRIGGERS = ['cron', 'manual', 'startup', 'webhook'] as const;
const WINDOWS = ['24h', '7d', '30d', 'all'] as const;

export function ScansFiltersBar({
  value, onChange,
}: { value: ScansFiltersValue; onChange: (next: ScansFiltersValue) => void }) {
  const { t } = useTranslation();
  const isDefault =
    value.status === SCANS_DEFAULTS.status &&
    value.trigger === SCANS_DEFAULTS.trigger &&
    value.window === SCANS_DEFAULTS.window;
  return (
    <div className="flex flex-wrap items-center gap-2" data-testid="scans-filters-bar">
      <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1">
        {t('scans.filtersLabel')}
      </span>
      <Select
        value={value.status}
        onValueChange={(v) => { if (v) onChange({ ...value, status: v }); }}
      >
        <SelectTrigger className="h-8 w-[140px] text-[12.5px]" aria-label={t('scans.statusFilterAria')}>
          <SelectValue placeholder={t('scans.anyStatus')} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">{t('scans.anyStatus')}</SelectItem>
          {STATUSES.map((s) => (
            <SelectItem key={s} value={s}>{t(`scans.status.${s}`, { defaultValue: s })}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select
        value={value.trigger}
        onValueChange={(v) => { if (v) onChange({ ...value, trigger: v }); }}
      >
        <SelectTrigger className="h-8 w-[140px] text-[12.5px]" aria-label={t('scans.triggerFilterAria')}>
          <SelectValue placeholder={t('scans.anyTrigger')} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">{t('scans.anyTrigger')}</SelectItem>
          {TRIGGERS.map((tg) => (
            <SelectItem key={tg} value={tg}>{t(`scans.trigger.${tg}`, { defaultValue: tg })}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select
        value={value.window}
        onValueChange={(v) => { if (v) onChange({ ...value, window: v }); }}
      >
        <SelectTrigger className="h-8 w-[120px] text-[12.5px]" aria-label={t('scans.windowFilterAria')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {WINDOWS.map((w) => (
            <SelectItem key={w} value={w}>{t(`scans.window.${w}`, { defaultValue: w })}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      <div className="flex-1" />
      <Button
        variant="ghost" size="sm"
        onClick={() => onChange(SCANS_DEFAULTS)}
        disabled={isDefault}
        data-testid="scans-filters-reset"
      >
        {t('scans.resetFilters')}
      </Button>
    </div>
  );
}
