import type { Control, FieldErrors, UseFormRegister } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Controller } from 'react-hook-form';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Input } from '@/components/ui/input';
import { SegmentedField } from './SegmentedField';
import {
  NumberField, TagListEditor,
} from '@/components/settings/instance-form-fields';

export interface TuningSectionProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<any, any, any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly register: UseFormRegister<any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly errors: FieldErrors<any>;
  readonly tValidationError: (msg: string | undefined) => string;
}

export function TuningSection({
  control, register, errors, tValidationError,
}: TuningSectionProps) {
  void register;
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-4" data-testid="tuning-section">
      {/* Cooldown segmented */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="cooldown-mode">{t('settings.instances.form.cooldownModeLabel')}</Label>
        <Controller
          control={control}
          name="cooldown_mode"
          render={({ field }) => (
            <SegmentedField
              id="cooldown-mode"
              value={field.value as string}
              onChange={(v) => field.onChange(v)}
              ariaLabel={t('settings.instances.form.cooldownModeLabel')}
              maxWidth={280}
              options={[
                { value: 'smart',  label: t('settings.instances.form.cooldownModes.smart') },
                { value: 'strict', label: t('settings.instances.form.cooldownModes.strict') },
              ]}
            />
          )}
        />
      </div>

      {/* Tags grid */}
      <div className="grid grid-cols-2 gap-3.5">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="inst-tags-include">
            {t('settings.instances.form.tuning.tagsIncludeLabel')}
          </Label>
          <Controller
            name="tags_include"
            control={control}
            render={({ field }) => (
              <TagListEditor
                id="inst-tags-include"
                value={field.value as readonly string[]}
                onChange={(next) => field.onChange([...next])}
                placeholder={t('settings.instances.form.tuning.tagsIncludePlaceholder')}
              />
            )}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="inst-tags-exclude">
            {t('settings.instances.form.tuning.tagsExcludeLabel')}
          </Label>
          <Controller
            name="tags_exclude"
            control={control}
            render={({ field }) => (
              <TagListEditor
                id="inst-tags-exclude"
                value={field.value as readonly string[]}
                onChange={(next) => field.onChange([...next])}
                placeholder={t('settings.instances.form.tuning.tagsExcludePlaceholder')}
              />
            )}
          />
        </div>
      </div>

      {/* Numeric grid (timeout, search-timeout, scan-max, CF threshold) */}
      <div className="grid grid-cols-2 gap-3.5">
        <NumberField
          control={control}
          name="timeout_sec"
          id="inst-timeout"
          label={t('settings.instances.form.timeoutLabel')}
          suffix={t('settings.instances.form.timeoutSuffix')}
          min={1} max={300}
          error={tValidationError(errors.timeout_sec?.message as string | undefined)}
        />
        <NumberField
          control={control}
          name="search_timeout_sec"
          id="inst-search-timeout"
          label={t('settings.instances.form.searchTimeoutLabel')}
          suffix={t('settings.instances.form.timeoutSuffix')}
          min={1} max={600}
          error={tValidationError(errors.search_timeout_sec?.message as string | undefined)}
        />
        <NumberField
          control={control}
          name="limits_scan_max_series"
          id="limits-scan-max"
          label={t('settings.instances.form.scanMaxSeriesLabel')}
          min={0} max={100000}
          hint={t('settings.instances.form.scanMaxSeriesHint')}
          error={tValidationError(errors.limits_scan_max_series?.message as string | undefined)}
        />
        <NumberField
          control={control}
          name="search_min_custom_format_score"
          id="search-mcfs"
          label={t('settings.instances.form.minCustomFormatScoreLabel')}
          min={-1000} max={1000}
          error={tValidationError(errors.search_min_custom_format_score?.message as string | undefined)}
        />
      </div>

      {/* Skip-anime field-row */}
      <div className="flex items-center justify-between gap-3 pt-1">
        <div className="flex flex-col gap-0.5">
          <span id="search-skip-anime-label" className="text-[13px] font-[550]">
            {t('settings.instances.form.skipAnimeLabel')}
          </span>
          <span className="text-[11.5px] text-tx-muted">
            {t('settings.instances.form.skipAnimeHint')}
          </span>
        </div>
        <Controller
          control={control}
          name="search_skip_anime"
          render={({ field }) => (
            <Switch
              id="search-skip-anime"
              aria-labelledby="search-skip-anime-label"
              checked={Boolean(field.value)}
              onCheckedChange={(v) => field.onChange(v)}
            />
          )}
        />
      </div>

      {/* Advanced sub-block */}
      <div
        data-testid="tuning-advanced"
        className="bg-base border-t border-border-faint -mx-[15px] px-[15px] pt-4 pb-1 flex flex-col gap-3.5"
      >
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-tx-faint">
          {t('settings.instances.form.tuning.advancedHeading')}
        </span>

        <div className="grid grid-cols-2 gap-3.5">
          <NumberField
            control={control}
            name="rate_limit_rpm"
            id="rate-limit-rpm"
            label={t('settings.instances.form.rateLimitRpmLabel')}
            suffix={t('settings.instances.form.rateLimitRpmSuffix')}
            min={0} max={10000}
            error={tValidationError(errors.rate_limit_rpm?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="rate_limit_burst"
            id="rate-limit-burst"
            label={t('settings.instances.form.rateLimitBurstLabel')}
            min={0} max={10000}
            error={tValidationError(errors.rate_limit_burst?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="limits_max_grabs_per_scan"
            id="limits-grabs"
            label={t('settings.instances.form.maxGrabsPerScanLabel')}
            min={0} max={100}
            error={tValidationError(errors.limits_max_grabs_per_scan?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="ranking_origin_bonus"
            id="ranking-origin-bonus"
            label={t('settings.instances.form.originBonusLabel')}
            min={-100} max={100} step={0.1}
            error={tValidationError(errors.ranking_origin_bonus?.message as string | undefined)}
          />
        </div>

        <div className="grid grid-cols-3 gap-3.5">
          <NumberField
            control={control}
            name="cooldown_series_after_grab_sec"
            id="cd-series"
            label={t('settings.instances.form.cdSeriesLabel')}
            suffix={t('settings.instances.form.timeoutSuffix')}
            min={0} max={604800}
            error={tValidationError(errors.cooldown_series_after_grab_sec?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="cooldown_guid_after_failed_grab_sec"
            id="cd-guid-grab"
            label={t('settings.instances.form.cdGuidGrabLabel')}
            suffix={t('settings.instances.form.timeoutSuffix')}
            min={0} max={604800}
            error={tValidationError(errors.cooldown_guid_after_failed_grab_sec?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="cooldown_guid_after_failed_import_sec"
            id="cd-guid-import"
            label={t('settings.instances.form.cdGuidImportLabel')}
            suffix={t('settings.instances.form.timeoutSuffix')}
            min={0} max={604800}
            error={tValidationError(errors.cooldown_guid_after_failed_import_sec?.message as string | undefined)}
          />
        </div>

        <div className="grid grid-cols-3 gap-3.5">
          <NumberField
            control={control}
            name="retry_max_attempts"
            id="retry-attempts"
            label={t('settings.instances.form.retryMaxAttemptsLabel')}
            min={0} max={10}
            error={tValidationError(errors.retry_max_attempts?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="retry_initial_backoff_sec"
            id="retry-initial"
            label={t('settings.instances.form.retryInitialBackoffLabel')}
            suffix={t('settings.instances.form.timeoutSuffix')}
            min={0} max={3600}
            error={tValidationError(errors.retry_initial_backoff_sec?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="retry_max_backoff_sec"
            id="retry-max"
            label={t('settings.instances.form.retryMaxBackoffLabel')}
            suffix={t('settings.instances.form.timeoutSuffix')}
            min={0} max={3600}
            error={tValidationError(errors.retry_max_backoff_sec?.message as string | undefined)}
          />
        </div>

        <div className="grid grid-cols-2 gap-3.5">
          <NumberField
            control={control}
            name="health_recheck_auth_sec"
            id="hc-auth"
            label={t('settings.instances.form.healthRecheckAuthLabel')}
            suffix={t('settings.instances.form.timeoutSuffix')}
            min={10} max={86400}
            error={tValidationError(errors.health_recheck_auth_sec?.message as string | undefined)}
          />
          <NumberField
            control={control}
            name="health_recheck_network_sec"
            id="hc-net"
            label={t('settings.instances.form.healthRecheckNetworkLabel')}
            suffix={t('settings.instances.form.timeoutSuffix')}
            min={10} max={86400}
            error={tValidationError(errors.health_recheck_network_sec?.message as string | undefined)}
          />
        </div>
      </div>
    </div>
  );
}

// Suppresses unused-import lint when callers don't render Input here;
// keeping the import lets future PRs add the search-only `<Input>` fields
// without a new merge. Safe no-op.
export const _UNUSED = Input;
