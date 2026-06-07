import { Controller, type Control } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Label } from '@/components/ui/label';
import { SegmentedField } from './SegmentedField';
import type { DryRunChoice } from '@/components/settings/instance-form-helpers';

export interface PromotedControlsProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<any, any, any>;
}

export function PromotedControls({ control }: PromotedControlsProps) {
  const { t } = useTranslation();
  return (
    <div className="grid grid-cols-2 gap-4" data-testid="promoted-controls">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="promoted-mode" className="text-[12.5px]">
          {t('settings.instances.form.modeLabel')}
        </Label>
        <Controller
          control={control}
          name="mode"
          render={({ field }) => (
            <SegmentedField
              id="promoted-mode"
              value={field.value as string}
              onChange={(v) => field.onChange(v)}
              ariaLabel={t('settings.instances.form.modeLabel')}
              options={[
                { value: 'auto',   label: t('settings.instances.form.promoted.mode.auto') },
                { value: 'manual', label: t('settings.instances.form.promoted.mode.manual') },
              ]}
            />
          )}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="promoted-dryrun" className="text-[12.5px]">
          {t('settings.instances.form.dryRunLabel')}
        </Label>
        <Controller
          control={control}
          name="dry_run"
          render={({ field }) => (
            <SegmentedField
              id="promoted-dryrun"
              value={field.value as DryRunChoice}
              onChange={(v) => field.onChange(v as DryRunChoice)}
              ariaLabel={t('settings.instances.form.dryRunLabel')}
              options={[
                { value: 'auto', label: t('settings.instances.form.promoted.dryRun.auto') },
                { value: 'off',  label: t('settings.instances.form.promoted.dryRun.off') },
                { value: 'on',   label: t('settings.instances.form.promoted.dryRun.on') },
              ]}
            />
          )}
        />
      </div>
    </div>
  );
}
