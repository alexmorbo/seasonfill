import type { Control, FieldErrors, UseFormRegister } from 'react-hook-form';
import { useWatch } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Controller } from 'react-hook-form';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Input } from '@/components/ui/input';
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select';
import type {
  InstanceMetadataQualityProfile, InstanceMetadataRootFolder,
} from '@/lib/instances-mutations';
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
  // ADR-0009 S7 — Add-to-Sonarr default pickers.
  readonly qualityProfiles?: readonly InstanceMetadataQualityProfile[];
  readonly rootFolders?: readonly InstanceMetadataRootFolder[];
  readonly metadataReady?: boolean;
  readonly metadataLoading?: boolean;
}

export function TuningSection({
  control, register, errors, tValidationError,
  qualityProfiles = [], rootFolders = [], metadataReady = false, metadataLoading = false,
}: TuningSectionProps) {
  void register;
  const { t } = useTranslation();
  // A3a: correct-by-construction — do not render, not merely disable.
  // search_skip_anime/skip_specials/require_all_aired are TV-only
  // concepts; skip_specials & require_all_aired have no UI control at
  // all today (dead fields, still round-tripped via zod defaults —
  // see story Non-goals), so only the live skip-anime block needs a
  // guard.
  const arrType = (useWatch({
    control,
    name: 'type',
  }) ?? 'sonarr') as 'sonarr' | 'radarr';
  return (
    <div className="flex flex-col gap-4" data-testid="tuning-section">
      {/* ADR-0009 S7 — Add-to-Sonarr defaults. Enabled only after a
          successful in-dialog Test populates the metadata lists. Soft
          validation: a saved id/path absent from the fresh list is shown
          as the placeholder ("cleared, not crashed") by passing the Radix
          Select an empty DISPLAY value — Radix only renders the placeholder
          for an empty controlled value, so a value with no matching item
          would otherwise render as blank text. The underlying RHF field
          value is left UNTOUCHED (gotcha #2), so an untouched save still
          round-trips the original id (no COALESCE guard on the BE PUT). */}
      <div className="flex flex-col gap-3.5" data-testid="tuning-defaults">
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-tx-faint">
          {t('settings.instances.form.tuning.defaultsHeading', {
            arr: t(`instances.type.${arrType === 'radarr' ? 'radarr' : 'sonarr'}`),
          })}
        </span>
        {!metadataReady && (
          <p className="text-[11.5px] text-tx-muted" data-testid="tuning-defaults-hint">
            {t('settings.instances.form.tuning.defaultsNeedTestHint')}
          </p>
        )}
        <div className="grid grid-cols-2 gap-3.5">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="inst-default-qp">
              {t('settings.instances.form.tuning.defaultQualityProfileLabel')}
            </Label>
            <Controller
              control={control}
              name="default_quality_profile_id"
              render={({ field }) => {
                const raw = field.value == null ? '' : String(field.value);
                const inList = qualityProfiles.some(
                  (qp) => qp.id != null && String(qp.id) === raw,
                );
                return (
                <Select
                  value={inList ? raw : ''}
                  onValueChange={(v) => field.onChange(v === '' ? null : Number(v))}
                  disabled={!metadataReady}
                >
                  <SelectTrigger id="inst-default-qp" data-testid="inst-default-qp">
                    <SelectValue
                      placeholder={metadataLoading
                        ? t('settings.instances.form.tuning.defaultsLoading')
                        : t('settings.instances.form.tuning.defaultQualityProfilePlaceholder')}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {qualityProfiles
                      .filter((qp) => qp.id != null)
                      .map((qp) => (
                        <SelectItem key={qp.id} value={String(qp.id)}>
                          {qp.name ?? String(qp.id)}
                        </SelectItem>
                      ))}
                  </SelectContent>
                </Select>
                );
              }}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="inst-default-rf">
              {t('settings.instances.form.tuning.defaultRootFolderLabel')}
            </Label>
            <Controller
              control={control}
              name="default_root_folder_path"
              render={({ field }) => {
                const raw = (field.value as string | null) ?? '';
                const inList = rootFolders.some(
                  (rf) => rf.path != null && rf.path !== '' && rf.path === raw,
                );
                return (
                <Select
                  value={inList ? raw : ''}
                  onValueChange={(v) => field.onChange(v === '' ? null : v)}
                  disabled={!metadataReady}
                >
                  <SelectTrigger id="inst-default-rf" data-testid="inst-default-rf">
                    <SelectValue
                      placeholder={metadataLoading
                        ? t('settings.instances.form.tuning.defaultsLoading')
                        : t('settings.instances.form.tuning.defaultRootFolderPlaceholder')}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {rootFolders
                      .filter((rf) => rf.path != null && rf.path !== '')
                      .map((rf) => (
                        <SelectItem
                          key={rf.id ?? rf.path}
                          value={rf.path as string}
                          disabled={rf.accessible === false}
                        >
                          {rf.path}
                        </SelectItem>
                      ))}
                  </SelectContent>
                </Select>
                );
              }}
            />
          </div>
        </div>

        {/* ADR-0023 A3b — radarr-only default minimumAvailability. NOT gated on
            metadataReady: unlike the QP/RF pickers this is a fixed Radarr enum,
            not a per-instance list, so no Test round-trip is required. Gated by
            render (not `disabled`) — correct-by-construction, same rule as the
            skip-anime row below. */}
        {arrType === 'radarr' && (
          <div className="flex flex-col gap-1.5" data-testid="tuning-default-minavail">
            <Label htmlFor="inst-default-minavail">
              {t('settings.instances.form.tuning.defaultMinAvailabilityLabel')}
            </Label>
            <Controller
              control={control}
              name="default_minimum_availability"
              render={({ field }) => (
                <SegmentedField
                  id="inst-default-minavail"
                  value={(field.value as string | null) ?? ''}
                  onChange={(v) => field.onChange(v)}
                  ariaLabel={t('settings.instances.form.tuning.defaultMinAvailabilityLabel')}
                  maxWidth={360}
                  options={[
                    { value: 'announced', label: t('movies.add.minAvail.announced') },
                    { value: 'inCinemas', label: t('movies.add.minAvail.inCinemas') },
                    { value: 'released',  label: t('movies.add.minAvail.released') },
                  ]}
                />
              )}
            />
            <p className="text-[11.5px] text-tx-muted">
              {t('settings.instances.form.tuning.defaultMinAvailabilityHint')}
            </p>
          </div>
        )}
      </div>

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

      {/* Skip-anime field-row — TV-only, hidden for radarr instances. */}
      {arrType !== 'radarr' && (
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
      )}

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
// reason: see comment above — intentional re-export to pin import
// eslint-disable-next-line react-refresh/only-export-components
export const _UNUSED = Input;
