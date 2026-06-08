import { useCallback, useMemo } from 'react';
import {
  Controller,
  type Control, type FieldErrors, type UseFormGetValues,
  type UseFormRegister, type UseFormSetValue, type UseFormWatch,
} from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import {
  Tooltip, TooltipContent, TooltipProvider, TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  NumberField, TagListEditor,
} from '@/components/settings/instance-form-fields';
import {
  AutoFillQbitButton,
  type AutoFillApplyResult, type AutoFillFields,
} from './AutoFillQbitButton';
import { useQbitSettings, useWebhookStatus } from '@/api/qbit';

export interface WatchdogSectionProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<any, any, any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly register: UseFormRegister<any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly errors: FieldErrors<any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly setValue: UseFormSetValue<any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly getValues: UseFormGetValues<any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly watch: UseFormWatch<any>;
  readonly mode: 'create' | 'edit';
  readonly instanceName: string | undefined;
  readonly tValidationError: (msg: string | undefined) => string;
}

export function WatchdogSection({
  control, register, errors, setValue, getValues, watch, mode, instanceName,
  tValidationError,
}: WatchdogSectionProps) {
  const { t } = useTranslation();
  const isCreate = mode === 'create';

  const webhookStatusQuery = useWebhookStatus(instanceName ?? '');
  const settingsQuery = useQbitSettings(instanceName ?? null);
  const webhookInstalled = Boolean(webhookStatusQuery.data?.installed);
  const passwordSet = Boolean(settingsQuery.data?.password_set);
  const enableLocked = isCreate || !webhookInstalled;

  const passwordValue = watch('qbit_password');
  const passwordPlaceholder = useMemo(() => {
    if (passwordSet && passwordValue === '') {
      return t('settings.instances.form.watchdog.form.password.placeholderSet');
    }
    return t('settings.instances.form.watchdog.form.password.placeholderUnset');
  }, [passwordSet, passwordValue, t]);

  // Stable callback — getValues + setValue are RHF refs that never
  // change identity. Compares discovered values against current
  // form state and only writes fields that actually differ. Returns
  // { changed: boolean } so the button can suppress the toast when
  // there is no real change (idempotent click).
  const onApply = useCallback((fields: AutoFillFields): AutoFillApplyResult => {
    let changed = false;
    if (fields.url !== undefined && fields.url !== getValues('qbit_url')) {
      setValue('qbit_url', fields.url, { shouldDirty: true });
      changed = true;
    }
    if (fields.username !== undefined
      && fields.username !== getValues('qbit_username')) {
      setValue('qbit_username', fields.username, { shouldDirty: true });
      changed = true;
    }
    if (fields.category !== undefined
      && fields.category !== getValues('qbit_category')) {
      setValue('qbit_category', fields.category, { shouldDirty: true });
      changed = true;
    }
    return { changed };
  }, [getValues, setValue]);

  return (
    <div className="flex flex-col gap-4" data-testid="watchdog-section">
      {!isCreate && instanceName && (
        <AutoFillQbitButton
          instanceName={instanceName}
          onApply={onApply}
        />
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="qbit-url">
          {t('settings.instances.form.watchdog.form.url.label')}
        </Label>
        <Input
          id="qbit-url"
          type="url"
          className="font-mono"
          placeholder={t('settings.instances.form.watchdog.form.url.placeholder')}
          aria-invalid={Boolean(errors.qbit_url) || undefined}
          {...register('qbit_url')}
        />
        <p className="text-[11.5px] text-tx-muted">
          {t('settings.instances.form.watchdog.form.url.help')}
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3.5">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="qbit-user">
            {t('settings.instances.form.watchdog.form.username.label')}
          </Label>
          <Input
            id="qbit-user"
            autoComplete="off"
            className="font-mono"
            {...register('qbit_username')}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="qbit-pass">
            {t('settings.instances.form.watchdog.form.password.label')}
          </Label>
          <Input
            id="qbit-pass"
            type="password"
            autoComplete="new-password"
            className="font-mono"
            placeholder={passwordPlaceholder}
            {...register('qbit_password')}
          />
        </div>
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="qbit-cat">
          {t('settings.instances.form.watchdog.form.category.label')}
        </Label>
        <Input id="qbit-cat" className="font-mono" {...register('qbit_category')} />
      </div>

      <div className="grid grid-cols-3 gap-3.5">
        <NumberField
          control={control}
          name="qbit_poll_interval_minutes"
          id="qbit-poll"
          label={t('settings.instances.form.watchdog.form.pollInterval.label')}
          min={5} max={1440}
          error={tValidationError(errors.qbit_poll_interval_minutes?.message as string | undefined)}
        />
        <NumberField
          control={control}
          name="qbit_regrab_cooldown_hours"
          id="qbit-cd"
          label={t('settings.instances.form.watchdog.form.regrabCooldown.label')}
          min={1} max={720}
          error={tValidationError(errors.qbit_regrab_cooldown_hours?.message as string | undefined)}
        />
        <NumberField
          control={control}
          name="qbit_max_consecutive_no_better"
          id="qbit-strike"
          label={t('settings.instances.form.watchdog.form.maxConsecutive.label')}
          min={1} max={100}
          error={tValidationError(errors.qbit_max_consecutive_no_better?.message as string | undefined)}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="qbit-custom-msgs">
          {t('settings.instances.form.watchdog.form.customMsgs.label')}
        </Label>
        <Controller
          control={control}
          name="qbit_custom_unregistered_msgs"
          render={({ field }) => (
            <TagListEditor
              id="qbit-custom-msgs"
              value={field.value as readonly string[]}
              onChange={(next) => field.onChange([...next])}
              placeholder={t('settings.instances.form.watchdog.form.customMsgs.addPlaceholder')}
            />
          )}
        />
        <p className="text-[11.5px] text-tx-muted">
          {t('settings.instances.form.watchdog.form.customMsgs.help')}
        </p>
      </div>

      <div className="flex items-start gap-3 pt-1">
        <Controller
          control={control}
          name="qbit_enabled"
          render={({ field }) => (
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="inline-flex">
                    <Switch
                      id="qbit-enabled"
                      checked={Boolean(field.value)}
                      onCheckedChange={(v) => field.onChange(v)}
                      disabled={enableLocked}
                      aria-label={t('settings.instances.form.watchdog.form.enabled.label')}
                    />
                  </span>
                </TooltipTrigger>
                {enableLocked && (
                  <TooltipContent>
                    {isCreate
                      ? t('settings.instances.form.watchdog.form.enabled.helpCreate')
                      : t('settings.instances.form.watchdog.form.enabled.helpDisabled')}
                  </TooltipContent>
                )}
              </Tooltip>
            </TooltipProvider>
          )}
        />
        <div className="flex flex-col gap-0.5">
          <Label htmlFor="qbit-enabled" className="font-normal">
            {t('settings.instances.form.watchdog.form.enabled.label')}
          </Label>
          <p className="text-[11.5px] text-tx-muted">
            {enableLocked
              ? (isCreate
                  ? t('settings.instances.form.watchdog.form.enabled.helpCreate')
                  : t('settings.instances.form.watchdog.form.enabled.helpDisabled'))
              : t('settings.instances.form.watchdog.form.enabled.helpEnabled')}
          </p>
        </div>
      </div>
    </div>
  );
}
