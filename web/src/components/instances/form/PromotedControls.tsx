import { Controller, type Control } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import { SegmentedField } from './SegmentedField';
import type { DryRunChoice } from '@/components/settings/instance-form-helpers';

export interface PromotedControlsProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<any, any, any>;
  readonly mode: 'create' | 'edit';
  // ADR-0023 F1 (BUG 2): fired with the new type whenever the create-mode
  // segmented control changes. Undefined/no-op in edit mode (the type
  // Controller there renders the read-only badge branch and never calls
  // this at all — see the isEdit check inside the Controller render).
  readonly onTypeChange?: ((v: 'sonarr' | 'radarr') => void) | undefined;
}

export function PromotedControls({ control, mode, onTypeChange }: PromotedControlsProps) {
  const { t } = useTranslation();
  const isEdit = mode === 'edit';
  return (
    <div className="flex flex-col gap-4">
      {/* Ф6-R-6b: arr kind. Chosen at creation, immutable afterwards. */}
      <div className="flex flex-col gap-1.5" data-testid="promoted-type">
        <Label htmlFor="promoted-type" className="text-[12.5px]">
          {t('settings.instances.form.typeLabel')}
        </Label>
        <Controller
          control={control}
          name="type"
          render={({ field }) => {
            const current = (field.value as string) === 'radarr' ? 'radarr' : 'sonarr';
            if (isEdit) {
              return (
                <div className="flex flex-col gap-1" data-testid="promoted-type-readonly">
                  <Badge variant="solid" mono className="w-fit">
                    {t(`settings.instances.form.type.${current}`)}
                  </Badge>
                  <span className="text-[11px] text-tx-faint">
                    {t('settings.instances.form.typeImmutableHint')}
                  </span>
                </div>
              );
            }
            return (
              <SegmentedField
                id="promoted-type"
                value={current}
                onChange={(v) => {
                  field.onChange(v);
                  onTypeChange?.(v === 'radarr' ? 'radarr' : 'sonarr');
                }}
                ariaLabel={t('settings.instances.form.typeLabel')}
                options={[
                  { value: 'sonarr', label: t('settings.instances.form.type.sonarr') },
                  { value: 'radarr', label: t('settings.instances.form.type.radarr') },
                ]}
              />
            );
          }}
        />
      </div>

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
    </div>
  );
}
